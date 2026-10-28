package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthClient performs a public-client login and refreshes its tokens.
type OAuthClient struct {
	ID               string
	AuthorizationURL string
	TokenURL         string
}

type authorizationResult struct {
	code string
	err  error
}

// Login waits for a loopback callback after open presents the authorization URL.
func (c OAuthClient) Login(ctx context.Context, open func(string) error) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if c.ID == "" {
		return Credential{}, errors.New("browser login is unavailable: this build has no OAuth client ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Credential{}, fmt.Errorf("start browser login callback: %w", err)
	}
	defer listener.Close()
	redirectURI := "http://" + listener.Addr().String() + "/callback"
	state := rand.Text()
	verifierBytes := make([]byte, 32)
	rand.Read(verifierBytes)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challenge := sha256.Sum256([]byte(verifier))
	authorizationURL, err := url.Parse(c.AuthorizationURL)
	if err != nil {
		return Credential{}, errors.New("invalid OAuth authorization endpoint")
	}
	query := authorizationURL.Query()
	query.Set("client_id", c.ID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "full")
	query.Set("state", state)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	authorizationURL.RawQuery = query.Encode()

	results := make(chan authorizationResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", callbackHandler(state, listener.Addr().String(), results))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	if err := open(authorizationURL.String()); err != nil {
		return Credential{}, err
	}
	select {
	case <-ctx.Done():
		return Credential{}, fmt.Errorf("browser login stopped; run sliplane auth login to try again: %w", ctx.Err())
	case err := <-serveErrors:
		return Credential{}, fmt.Errorf("browser login callback stopped: %w", err)
	case result := <-results:
		if result.err != nil {
			return Credential{}, result.err
		}
		return c.exchange(ctx, result.code, verifier, redirectURI)
	}
}

// callbackHandler ignores invalid state so an unrelated request cannot stop login.
func callbackHandler(state, host string, results chan<- authorizationResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || r.Host != host || len(query["state"]) != 1 || query.Get("state") != state {
			http.Error(w, "Invalid login callback.", http.StatusBadRequest)
			return
		}
		if len(query["code"]) > 1 || len(query["error"]) > 1 || (query.Has("code") && query.Has("error")) {
			http.Error(w, "Invalid login callback.", http.StatusBadRequest)
			return
		}
		result := authorizationResult{code: query.Get("code")}
		if query.Has("error") {
			result.err = errors.New("browser authorization was denied or failed; run sliplane auth login to try again")
		} else if result.code == "" {
			http.Error(w, "Missing authorization code.", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "Authorization received. Return to your terminal to check the login result.")
		_ = http.NewResponseController(w).Flush()
		select {
		case results <- result:
		default:
		}
	}
}

// exchange omits client authentication and never includes provider bodies in errors.
func (c OAuthClient) exchange(ctx context.Context, code, verifier, redirectURI string) (Credential, error) {
	return c.requestToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.ID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}, "OAuth token exchange failed")
}

// Refresh exchanges a refresh token for a new token pair; the old pair stops working.
func (c OAuthClient) Refresh(ctx context.Context, refreshToken string) (Credential, error) {
	return c.requestToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ID},
		"refresh_token": {refreshToken},
	}, "browser login expired")
}

func (c OAuthClient) requestToken(ctx context.Context, form url.Values, failure string) (Credential, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Credential{}, errors.New("invalid OAuth token endpoint")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Credential{}, ctx.Err()
		}
		return Credential{}, errors.New(failure + "; run sliplane auth login to try again")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Credential{}, fmt.Errorf("%s (HTTP %d); run sliplane auth login to try again", failure, response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&token); err != nil {
		return Credential{}, errors.New("invalid OAuth token response")
	}
	if !strings.EqualFold(token.TokenType, "Bearer") {
		return Credential{}, errors.New("OAuth token response requires a bearer token")
	}
	accessToken, _, err := validate(token.AccessToken, "OAuth")
	if err != nil {
		return Credential{}, err
	}
	if token.RefreshToken != "" {
		if _, _, err := validate(token.RefreshToken, "OAuth refresh"); err != nil {
			return Credential{}, err
		}
	}
	return Credential{AccessToken: accessToken, RefreshToken: token.RefreshToken}, nil
}
