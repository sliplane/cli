package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowedSignersMatchRepository(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "allowed_signers"))
	if err != nil {
		t.Fatal(err)
	}
	var signers strings.Builder
	for line := range strings.Lines(string(contents)) {
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			signers.WriteString(line)
		}
	}
	if allowedSigners != signers.String() {
		t.Fatal("internal/update/allowed_signers differs from the keys in .github/allowed_signers")
	}
}

// The v0.6.0 checksums were signed with a maintainer YubiKey.
func TestVerifyReleaseSignature(t *testing.T) {
	checksums, err := os.ReadFile(filepath.Join("testdata", "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join("testdata", "checksums.txt.sig"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := trustedKeys(allowedSigners)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignature(checksums, signature, keys); err != nil {
		t.Fatalf("release signature rejected: %v", err)
	}
	tampered := append([]byte("0000  sliplane_0.6.0_linux_amd64.tar.gz\n"), checksums...)
	if err := verifySignature(tampered, signature, keys); err == nil || err.Error() != "signature does not match" {
		t.Fatalf("tampered checksums: %v", err)
	}
	if err := verifySignature(checksums, signature, keys[1:]); err == nil || !strings.Contains(err.Error(), "not from a trusted") {
		t.Fatalf("untrusted key: %v", err)
	}
}

func TestVerifySignature(t *testing.T) {
	signer := newTestSigner(t)
	message := []byte("checksums\n")
	for _, test := range []struct {
		name      string
		signature []byte
		wantError string
	}{
		{name: "sha512", signature: signer.sign(message, signatureNamespace, "sha512", userPresent)},
		{name: "sha256", signature: signer.sign(message, signatureNamespace, "sha256", userPresent)},
		{name: "other namespace", signature: signer.sign(message, "git", "sha512", userPresent), wantError: "not Sliplane releases"},
		{name: "no touch", signature: signer.sign(message, signatureNamespace, "sha512", 0), wantError: "without touching"},
		{name: "unsupported hash", signature: signer.sign(message, signatureNamespace, "md5", userPresent), wantError: "unsupported signature hash"},
		{name: "not armored", signature: []byte("signature"), wantError: "malformed signature"},
		{name: "truncated", signature: []byte("-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n"), wantError: "malformed signature"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifySignature(message, test.signature, [][]byte{signer.publicKey})
			if test.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("error %v; want %q", err, test.wantError)
			}
		})
	}
}

// testSigner signs like `ssh-keygen -Y sign` with a security key, using a software key.
type testSigner struct {
	privateKey ed25519.PrivateKey
	publicKey  []byte
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var key []byte
	for _, field := range [][]byte{[]byte(securityKeyType), public, []byte("ssh:test")} {
		key = appendString(key, field)
	}
	return testSigner{privateKey: private, publicKey: key}
}

func (s testSigner) allowedSigners() string {
	return `sliplane-release namespaces="sliplane-release" ` + securityKeyType + " " + base64.StdEncoding.EncodeToString(s.publicKey) + " test\n"
}

func (s testSigner) sign(message []byte, namespace, hashAlgorithm string, flags byte) []byte {
	var digest []byte
	if hashAlgorithm == "sha256" {
		sum := sha256.Sum256(message)
		digest = sum[:]
	} else {
		sum := sha512.Sum512(message)
		digest = sum[:]
	}
	wrapped := []byte("SSHSIG")
	for _, field := range []string{namespace, "", hashAlgorithm, string(digest)} {
		wrapped = appendString(wrapped, []byte(field))
	}
	applicationHash := sha256.Sum256([]byte("ssh:test"))
	wrappedHash := sha256.Sum256(wrapped)
	authenticator := append(append(applicationHash[:], flags), 0, 0, 0, 7)
	raw := ed25519.Sign(s.privateKey, append(authenticator, wrappedHash[:]...))

	signed := appendString(nil, []byte(securityKeyType))
	signed = appendString(signed, raw)
	signed = append(signed, flags, 0, 0, 0, 7)
	blob := binary.BigEndian.AppendUint32([]byte("SSHSIG"), 1)
	for _, field := range [][]byte{s.publicKey, []byte(namespace), nil, []byte(hashAlgorithm), signed} {
		blob = appendString(blob, field)
	}
	return []byte("-----BEGIN SSH SIGNATURE-----\n" + base64.StdEncoding.EncodeToString(blob) + "\n-----END SSH SIGNATURE-----\n")
}
