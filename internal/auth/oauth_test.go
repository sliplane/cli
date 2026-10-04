package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOAuthLoginExchangesPKCECode(t *testing.T) {
	authorizations := make(chan url.Values, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/token" {
			t.Errorf("unexpected token request: %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		authorization := <-authorizations
		verifier := r.Form.Get("code_verifier")
		challenge := sha256.Sum256([]byte(verifier))
		if len(verifier) < 43 || len(verifier) > 128 || base64.RawURLEncoding.EncodeToString(challenge[:]) != authorization.Get("code_challenge") {
			t.Error("token exchange did not prove the authorization challenge")
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != "public-client" || r.Form.Get("code") != "authorization-code" || r.Form.Get("redirect_uri") != authorization.Get("redirect_uri") {
			t.Error("incorrect token exchange parameters")
		}
		if r.Form.Has("client_secret") || r.Header.Get("Authorization") != "" {
			t.Error("public client supplied client authentication")
		}
		fmt.Fprint(w, `{"access_token":"access-token","token_type":"Bearer","refresh_token":"unused-refresh-token","expires_in":86400}`)
	}))
	defer provider.Close()
	client := OAuthClient{ID: "public-client", AuthorizationURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token"}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var callbackURL string
	token, err := client.Login(ctx, func(address string) error {
		authorizationURL, err := url.Parse(address)
		if err != nil {
			return err
		}
		query := authorizationURL.Query()
		if authorizationURL.Path != "/authorize" || query.Get("client_id") != client.ID || query.Get("scope") != "full" || query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" || len(query.Get("state")) < 26 {
			t.Error("incorrect authorization parameters")
		}
		callback, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			return err
		}
		if callback.Scheme != "http" || callback.Hostname() != "127.0.0.1" || callback.Port() == "" || callback.Path != "/callback" {
			t.Error("callback must use an ephemeral IPv4 loopback port")
		}
		callbackURL = callback.String()
		authorizations <- query
		// An unrelated callback must not consume the real login attempt.
		response, err := http.Get(callbackURL + "?state=wrong&code=wrong")
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Error("invalid state accepted")
		}
		callback.RawQuery = url.Values{"state": {query.Get("state")}, "code": {"authorization-code"}}.Encode()
		response, err = http.Get(callback.String())
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK || strings.Contains(string(body), "authorization-code") {
			t.Error("unsafe or unsuccessful browser response")
		}
		return err
	})
	if err != nil || token != "access-token" {
		t.Fatalf("login returned %q, %v", token, err)
	}
	response, err := http.Get(callbackURL)
	if err == nil {
		response.Body.Close()
		t.Fatal("callback listener remained open after login")
	}
}

func TestOAuthCallbackValidation(t *testing.T) {
	for _, test := range []struct {
		name, query, host string
		wantStatus        int
		wantResult        bool
		wantError         bool
	}{
		{name: "missing state", query: "code=code", wantStatus: 400},
		{name: "wrong state", query: "state=other&code=code", wantStatus: 400},
		{name: "repeated state", query: "state=expected&state=expected&code=code", wantStatus: 400},
		{name: "repeated code", query: "state=expected&code=code&code=other", wantStatus: 400},
		{name: "repeated error", query: "state=expected&error=denied&error=other", wantStatus: 400},
		{name: "code and error", query: "state=expected&code=code&error=denied", wantStatus: 400},
		{name: "missing code", query: "state=expected", wantStatus: 400},
		{name: "malformed query", query: "state=expected&code=%zz", wantStatus: 400},
		{name: "wrong host", query: "state=expected&code=code", host: "example.com", wantStatus: 400},
		{name: "denied", query: "state=expected&error=access_denied&error_description=secret", wantStatus: 200, wantResult: true, wantError: true},
		{name: "approved", query: "state=expected&code=code", wantStatus: 200, wantResult: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			results := make(chan authorizationResult, 1)
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/callback?"+test.query, nil)
			if test.host != "" {
				request.Host = test.host
			}
			response := httptest.NewRecorder()
			callbackHandler("expected", "127.0.0.1:1234", results)(response, request)
			if response.Code != test.wantStatus || (len(results) == 1) != test.wantResult {
				t.Fatalf("status %d, results %d", response.Code, len(results))
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("callback response can cache or leak its URL")
			}
			if test.wantResult {
				result := <-results
				if (result.err != nil) != test.wantError || (result.err != nil && strings.Contains(result.err.Error(), "secret")) {
					t.Fatalf("unexpected callback result: %+v", result)
				}
			}
		})
	}
}

func TestOAuthLoginStopsAndClosesCallback(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "open failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			openError := errors.New("cannot present login")
			client := OAuthClient{ID: "client", AuthorizationURL: "https://example.com/authorize"}
			var callbackURL string
			_, err := client.Login(ctx, func(address string) error {
				authorizationURL, _ := url.Parse(address)
				callbackURL = authorizationURL.Query().Get("redirect_uri")
				if mode == "open failure" {
					return openError
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			})
			want := context.Canceled
			if mode == "deadline" {
				want = context.DeadlineExceeded
			}
			if mode == "open failure" {
				want = openError
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			response, err := http.Get(callbackURL)
			if err == nil {
				response.Body.Close()
				t.Fatal("callback listener remained open")
			}
		})
	}
}

func TestOAuthTokenResponseValidation(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"provider error", `{"error":"secret"}`, 400},
		{"redirect", `secret`, 307},
		{"invalid JSON", `secret`, 200},
		{"missing token", `{"token_type":"Bearer"}`, 200},
		{"wrong token type", `{"access_token":"secret","token_type":"Basic"}`, 200},
		{"unsafe token", `{"access_token":"secret\nvalue","token_type":"Bearer"}`, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://127.0.0.1:1/secret")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer provider.Close()
			client := OAuthClient{ID: "client", TokenURL: provider.URL}
			token, err := client.exchange(t.Context(), "secret-code", "secret-verifier", "http://127.0.0.1/callback")
			if err == nil || token != "" || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe token exchange result: %q, %v", token, err)
			}
		})
	}
}
