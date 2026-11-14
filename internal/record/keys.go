// Package record signs a finished session's event log and checks it
// later: the signing key, the checkpoint over the log's last hash, and the
// attestation a pull request can carry.
package record

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	privateFile = "signing-key.pem"
	publicFile  = "signing-key.pub"
)

// KeyDir is where the signing key lives: the rehearsal folder in the
// user's configuration directory.
func KeyDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find your configuration directory for the signing key: %w", err)
	}
	return filepath.Join(dir, "rehearsal"), nil
}

// LoadOrCreateKey reads the signing key in dir, making a new one the
// first time. The private key file is readable by its owner only, and a
// key that anyone else can read is refused.
func LoadOrCreateKey(dir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dir, privateFile)
	key, err := loadPrivate(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create the folder for the signing key: %w", err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate the signing key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode the signing key: %w", err)
	}
	// O_EXCL: two sessions starting together must not each write a key
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return loadPrivate(path)
	}
	if err != nil {
		return nil, fmt.Errorf("write the signing key: %w", err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		return nil, errors.Join(fmt.Errorf("write the signing key: %w", err), f.Close())
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("write the signing key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("encode the public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	if err := os.WriteFile(filepath.Join(dir, publicFile), pubPEM, 0o644); err != nil {
		return nil, fmt.Errorf("write the public key: %w", err)
	}
	return key, nil
}

func loadPrivate(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read the signing key: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("the signing key %s can be read by other users (mode %04o). Run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the signing key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("the signing key %s is not a PEM private key", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("read the signing key %s: %w", path, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the signing key %s is not an Ed25519 key", path)
	}
	return key, nil
}

// OwnPublicKey is the public half of the key in dir. It never creates one.
func OwnPublicKey(dir string) (ed25519.PublicKey, error) {
	return ParsePublicKey(filepath.Join(dir, publicFile))
}

// ParsePublicKey reads a public key given either as a file (PEM, as
// written beside the signing key) or as the base64 text an attestation
// carries.
func ParsePublicKey(pathOrText string) (ed25519.PublicKey, error) {
	text := pathOrText
	// anything that cannot be read as a file is taken to be the key itself
	if data, err := os.ReadFile(pathOrText); err == nil {
		text = string(data)
	}
	text = strings.TrimSpace(text)
	if block, _ := pem.Decode([]byte(text)); block != nil {
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("read the public key: %w", err)
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("the public key is not an Ed25519 key")
		}
		return key, nil
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("the public key is neither a PEM file nor 32 bytes of base64")
	}
	return ed25519.PublicKey(raw), nil
}

// KeyText is a public key as base64, the form checkpoints and
// attestations carry.
func KeyText(key ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key)
}

// Fingerprint is a short name for a public key, for messages.
func Fingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}
