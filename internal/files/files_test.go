package files

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	s, err := New(t.TempDir(), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.4 hello")
	b, ct, err := Sniff(bytes.NewReader(data))
	if err != nil || ct != "application/pdf" {
		t.Fatal(err, ct)
	}
	k, err := s.Put(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal(err)
	}
	if err := s.Delete(k); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(k); err == nil {
		t.Fatal("deleted file readable")
	}
	if _, _, err := Sniff(bytes.NewReader([]byte("plain text"))); err != ErrType {
		t.Fatal("text accepted")
	}
	if _, err := s.Get("../../etc/passwd"); err == nil {
		t.Fatal("path traversal")
	}
}
