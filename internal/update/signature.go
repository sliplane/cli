package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
)

const (
	signatureNamespace = "sliplane-release"
	securityKeyType    = "sk-ssh-ed25519@openssh.com"
	userPresent        = 0x01
)

// allowedSigners lists the YubiKeys trusted to sign releases. It must match
// .github/allowed_signers; the tests enforce this.
//
//go:embed allowed_signers
var allowedSigners string

// trustedKeys returns the public key blobs of the security keys in an allowed_signers file.
func trustedKeys(signers string) ([][]byte, error) {
	var keys [][]byte
	for line := range strings.Lines(signers) {
		fields := strings.Fields(line)
		for index, field := range fields[:max(len(fields)-1, 0)] {
			if field != securityKeyType {
				continue
			}
			key, err := base64.StdEncoding.DecodeString(fields[index+1])
			if err != nil {
				return nil, fmt.Errorf("invalid release signer: %w", err)
			}
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("no trusted release signers")
	}
	return keys, nil
}

// verifySignature checks a signature from `ssh-keygen -Y sign -n sliplane-release`
// made by one of the trusted security keys, following OpenSSH's PROTOCOL.sshsig
// and PROTOCOL.u2f. Like `ssh-keygen -Y verify`, it requires a touch on the key.
func verifySignature(message, armored []byte, trusted [][]byte) error {
	blob, err := unarmor(armored)
	if err != nil {
		return err
	}
	signature := &reader{data: blob}
	magic := signature.next(6)
	version := signature.uint32()
	publicKey := signature.string()
	namespace := signature.string()
	reserved := signature.string()
	hashAlgorithm := signature.string()
	signed := signature.string()
	if signature.err != nil || len(signature.data) != 0 || string(magic) != "SSHSIG" || version != 1 {
		return errors.New("malformed signature")
	}
	if string(namespace) != signatureNamespace {
		return fmt.Errorf("signature is for %q, not Sliplane releases", namespace)
	}
	trustedKey := false
	for _, key := range trusted {
		trustedKey = trustedKey || bytes.Equal(key, publicKey)
	}
	if !trustedKey {
		return errors.New("signature is not from a trusted release key")
	}

	key := &reader{data: publicKey}
	keyType := key.string()
	keyBytes := key.string()
	application := key.string()
	if key.err != nil || len(key.data) != 0 || string(keyType) != securityKeyType || len(keyBytes) != ed25519.PublicKeySize {
		return errors.New("malformed release key")
	}

	var digest hash.Hash
	switch string(hashAlgorithm) {
	case "sha256":
		digest = sha256.New()
	case "sha512":
		digest = sha512.New()
	default:
		return fmt.Errorf("unsupported signature hash %q", hashAlgorithm)
	}
	digest.Write(message)
	var wrapped []byte
	wrapped = append(wrapped, "SSHSIG"...)
	for _, field := range [][]byte{namespace, reserved, hashAlgorithm, digest.Sum(nil)} {
		wrapped = appendString(wrapped, field)
	}

	inner := &reader{data: signed}
	signatureType := inner.string()
	signatureBytes := inner.string()
	flags := inner.next(1)
	counter := inner.next(4)
	if inner.err != nil || len(inner.data) != 0 || string(signatureType) != securityKeyType || len(signatureBytes) != ed25519.SignatureSize {
		return errors.New("malformed signature")
	}
	if flags[0]&userPresent == 0 {
		return errors.New("signature was made without touching the security key")
	}
	// Security keys sign the hashes of the application and message with the authenticator data.
	applicationHash := sha256.Sum256(application)
	wrappedHash := sha256.Sum256(wrapped)
	var data []byte
	data = append(data, applicationHash[:]...)
	data = append(data, flags...)
	data = append(data, counter...)
	data = append(data, wrappedHash[:]...)
	if !ed25519.Verify(keyBytes, data, signatureBytes) {
		return errors.New("signature does not match")
	}
	return nil
}

func unarmor(armored []byte) ([]byte, error) {
	text := strings.TrimSpace(string(armored))
	body, found := strings.CutPrefix(text, "-----BEGIN SSH SIGNATURE-----")
	body, ended := strings.CutSuffix(body, "-----END SSH SIGNATURE-----")
	if !found || !ended {
		return nil, errors.New("malformed signature")
	}
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return nil, errors.New("malformed signature")
	}
	return blob, nil
}

// reader decodes SSH wire format, remembering the first error.
type reader struct {
	data []byte
	err  error
}

func (r *reader) next(size int) []byte {
	if r.err != nil || size > len(r.data) {
		r.err = errors.New("truncated")
		return nil
	}
	value := r.data[:size]
	r.data = r.data[size:]
	return value
}

func (r *reader) uint32() uint32 {
	value := r.next(4)
	if value == nil {
		return 0
	}
	return binary.BigEndian.Uint32(value)
}

func (r *reader) string() []byte {
	size := r.uint32()
	if uint64(size) > uint64(len(r.data)) {
		r.err = errors.New("truncated")
		return nil
	}
	return r.next(int(size))
}

func appendString(data, value []byte) []byte {
	data = binary.BigEndian.AppendUint32(data, uint32(len(value)))
	return append(data, value...)
}
