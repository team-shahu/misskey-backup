package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

const (
	// AES-CTR用Nonceサイズ
	nonceSize = aes.BlockSize
	// HMAC-SHA256のタグサイズ
	authTagSize = sha256.Size
	// コピー用バッファサイズ
	encryptionBuf = 64 * 1024
)

// DeriveEncryptionKeys 入力されたキー素材（平文またはBase64）から暗号鍵とHMAC鍵を導出
// 32バイト以上のエントロピーを要求
func DeriveEncryptionKeys(keyMaterial string) ([]byte, []byte, error) {
	if keyMaterial == "" {
		return nil, nil, fmt.Errorf("encryption key is not set in BACKUP_ENCRYPTION_KEY")
	}

	decoded, err := base64.StdEncoding.DecodeString(keyMaterial)
	if err != nil || len(decoded) == 0 {
		decoded = []byte(keyMaterial)
	}

	if len(decoded) < 32 {
		return nil, nil, fmt.Errorf("encryption key must be at least 32 bytes after base64 decoding")
	}

	mainKey := sha256.Sum256(decoded) // derive 32-byte AES key
	hmacKey := sha256.Sum256(append(mainKey[:], byte('h'), byte('m'), byte('a'), byte('c')))

	return mainKey[:], hmacKey[:], nil
}

// EncryptFile AES-CTRで暗号化し、HMAC-SHA256のタグを末尾に付与
func EncryptFile(inputPath, outputPath string, encKey, hmacKey []byte) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer in.Close()

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create encrypted file: %w", err)
	}
	defer out.Close()

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	if _, err := out.Write(nonce); err != nil {
		return fmt.Errorf("failed to write nonce: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	stream := cipher.NewCTR(block, nonce)
	mac := hmac.New(sha256.New, hmacKey)
	if _, err := mac.Write(nonce); err != nil {
		return fmt.Errorf("failed to update HMAC with nonce: %w", err)
	}

	buf := make([]byte, encryptionBuf)
	for {
		n, readErr := in.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			stream.XORKeyStream(chunk, chunk)

			if _, err := out.Write(chunk); err != nil {
				return fmt.Errorf("failed to write encrypted chunk: %w", err)
			}
			if _, err := mac.Write(chunk); err != nil {
				return fmt.Errorf("failed to update HMAC with chunk: %w", err)
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read source file: %w", readErr)
		}
	}

	if _, err := out.Write(mac.Sum(nil)); err != nil {
		return fmt.Errorf("failed to write auth tag: %w", err)
	}

	if err := out.Sync(); err != nil {
		return fmt.Errorf("failed to sync encrypted file: %w", err)
	}

	return nil
}

// DecryptFile AES-CTRで復号し、HMAC検証を通過した場合のみ書き出しを残す
func DecryptFile(inputPath, outputPath string, encKey, hmacKey []byte) (err error) {
	in, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open encrypted file: %w", err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat encrypted file: %w", err)
	}
	if info.Size() < nonceSize+authTagSize {
		return fmt.Errorf("encrypted file is too small to contain nonce and tag")
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create decrypted file: %w", err)
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(outputPath)
		}
	}()

	nonce := make([]byte, nonceSize)
	if _, err = io.ReadFull(in, nonce); err != nil {
		return fmt.Errorf("failed to read nonce: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}
	stream := cipher.NewCTR(block, nonce)

	mac := hmac.New(sha256.New, hmacKey)
	if _, err := mac.Write(nonce); err != nil {
		return fmt.Errorf("failed to update HMAC with nonce: %w", err)
	}

	cipherLen := info.Size() - nonceSize - authTagSize
	limitedReader := io.LimitReader(in, cipherLen)

	buf := make([]byte, encryptionBuf)
	for {
		n, readErr := limitedReader.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, err := mac.Write(chunk); err != nil {
				return fmt.Errorf("failed to update HMAC with chunk: %w", err)
			}

			stream.XORKeyStream(chunk, chunk)
			if _, err := out.Write(chunk); err != nil {
				return fmt.Errorf("failed to write decrypted chunk: %w", err)
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read encrypted file: %w", readErr)
		}
	}

	tag := make([]byte, authTagSize)
	if _, err := io.ReadFull(in, tag); err != nil {
		return fmt.Errorf("failed to read auth tag: %w", err)
	}

	if !hmac.Equal(mac.Sum(nil), tag) {
		return fmt.Errorf("authentication failed: HMAC mismatch")
	}

	if err := out.Sync(); err != nil {
		return fmt.Errorf("failed to sync decrypted file: %w", err)
	}

	return nil
}
