package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func testKeys(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32))
	enc, hmac, err := DeriveEncryptionKeys(key)
	if err != nil {
		t.Fatalf("DeriveEncryptionKeys: %v", err)
	}
	return enc, hmac
}

func TestDeriveEncryptionKeys(t *testing.T) {
	if _, _, err := DeriveEncryptionKeys(""); err == nil {
		t.Error("empty key should error")
	}
	if _, _, err := DeriveEncryptionKeys("short"); err == nil {
		t.Error("short key should error")
	}
	enc, hmac := testKeys(t)
	if len(enc) != 32 || len(hmac) != 32 {
		t.Errorf("key sizes: enc=%d hmac=%d", len(enc), len(hmac))
	}
	if bytes.Equal(enc, hmac) {
		t.Error("enc and hmac keys should differ")
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	enc, hmac := testKeys(t)
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "plain.bin")
	encPath := filepath.Join(dir, "plain.bin.enc")
	outPath := filepath.Join(dir, "plain.out")

	// バッファ境界(64KB)を跨ぐサイズ
	want := make([]byte, 64*1024*2+123)
	if _, err := rand.Read(want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainPath, want, 0600); err != nil {
		t.Fatal(err)
	}

	if err := EncryptFile(plainPath, encPath, enc, hmac); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}
	if err := DecryptFile(encPath, outPath, enc, hmac); err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("roundtrip content mismatch")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	enc, hmac := testKeys(t)
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "p")
	encPath := filepath.Join(dir, "p.enc")
	outPath := filepath.Join(dir, "p.out")

	if err := os.WriteFile(plainPath, []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptFile(plainPath, encPath, enc, hmac); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(encPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	if err := DecryptFile(encPath, outPath, enc, hmac); err == nil {
		t.Error("tampered ciphertext should fail HMAC verification")
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Error("output should be removed on failure")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	enc, hmac := testKeys(t)
	other, otherH, err := DeriveEncryptionKeys(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 32)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "p")
	encPath := filepath.Join(dir, "p.enc")
	outPath := filepath.Join(dir, "p.out")

	if err := os.WriteFile(plainPath, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptFile(plainPath, encPath, enc, hmac); err != nil {
		t.Fatal(err)
	}
	if err := DecryptFile(encPath, outPath, other, otherH); err == nil {
		t.Error("wrong key should fail HMAC verification")
	}
}
