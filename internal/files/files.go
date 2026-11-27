// Package files stores uploaded documents encrypted at rest (AES-256-GCM).
package files

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MaxSize is the largest file accepted (10 MB).
const MaxSize = 10 << 20

var ErrTooLarge = errors.New("file is larger than 10 MB")
var ErrType = errors.New("only PDF, JPG or PNG files are accepted")

// Store keeps encrypted blobs on local disk.
type Store struct {
	dir  string
	aead cipher.AEAD
}

func New(dir string, key []byte) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, aead: aead}, nil
}

// Sniff reads the file, checks size and type, and returns its bytes and content type.
func Sniff(r io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxSize {
		return nil, "", ErrTooLarge
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("the file is empty")
	}
	ct := http.DetectContentType(data)
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		ct = "application/pdf"
	case strings.HasPrefix(ct, "image/jpeg"), strings.HasPrefix(ct, "image/png"):
	default:
		return nil, "", ErrType
	}
	return data, ct, nil
}

func (s *Store) path(key string) (string, error) {
	if len(key) != 32 || strings.ContainsAny(key, "./\\") {
		return "", fmt.Errorf("bad storage key")
	}
	return filepath.Join(s.dir, key[:2], key), nil
}

// Put encrypts and writes data, returning a new random storage key.
func (s *Store) Put(data []byte) (string, error) {
	kb := make([]byte, 16)
	if _, err := rand.Read(kb); err != nil {
		return "", err
	}
	key := hex.EncodeToString(kb)
	p, _ := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, data, []byte(key))
	if err := os.WriteFile(p, out, 0o600); err != nil {
		return "", err
	}
	return key, nil
}

// Get reads and decrypts a blob.
func (s *Store) Get(key string) ([]byte, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return nil, fmt.Errorf("corrupt file")
	}
	return s.aead.Open(nil, raw[:ns], raw[ns:], []byte(key))
}

// Delete removes a blob; deleting a missing blob is not an error.
func (s *Store) Delete(key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
