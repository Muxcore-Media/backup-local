package internal

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	encryptionv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/encryption/v1"
)

const backupEncryptMagic = "MXBK1"

type archiveCryptor struct {
	localAEAD cipher.AEAD
	encClient encryptionv1.EncryptionServiceClient
}

func newArchiveCryptor(keyHex string, encClient encryptionv1.EncryptionServiceClient) (*archiveCryptor, error) {
	keyHex = strings.TrimSpace(keyHex)
	if keyHex == "" && encClient == nil {
		return nil, nil
	}
	c := &archiveCryptor{encClient: encClient}
	if keyHex != "" {
		raw, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, fmt.Errorf("decode BACKUP_ENCRYPT_KEY: %w", err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("BACKUP_ENCRYPT_KEY must be 32 bytes (64 hex chars), got %d bytes", len(raw))
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		c.localAEAD = aead
	}
	return c, nil
}

func (c *archiveCryptor) enabled() bool {
	return c != nil && (c.localAEAD != nil || c.encClient != nil)
}

func (c *archiveCryptor) encryptFile(ctx context.Context, plainPath, encPath string) error {
	plain, err := os.ReadFile(plainPath)
	if err != nil {
		return err
	}
	out, err := c.encrypt(ctx, plain)
	if err != nil {
		return err
	}
	tmp := encPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, encPath)
}

func (c *archiveCryptor) decryptFile(ctx context.Context, encPath, plainPath string) error {
	data, err := os.ReadFile(encPath)
	if err != nil {
		return err
	}
	out, err := c.decrypt(ctx, data)
	if err != nil {
		return err
	}
	tmp := plainPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, plainPath)
}

func (c *archiveCryptor) encrypt(ctx context.Context, plain []byte) ([]byte, error) {
	if c.localAEAD != nil {
		nonce := make([]byte, c.localAEAD.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		sealed := c.localAEAD.Seal(nil, nonce, plain, []byte(backupEncryptMagic))
		out := make([]byte, 0, len(backupEncryptMagic)+len(nonce)+len(sealed))
		out = append(out, backupEncryptMagic...)
		out = append(out, nonce...)
		out = append(out, sealed...)
		return out, nil
	}
	if c.encClient != nil {
		resp, err := c.encClient.Encrypt(ctx, &encryptionv1.EncryptRequest{Plaintext: plain})
		if err != nil {
			return nil, fmt.Errorf("encryption encrypt: %w", err)
		}
		return resp.GetCiphertext(), nil
	}
	return nil, errors.New("no encryption backend configured")
}

func (c *archiveCryptor) decrypt(ctx context.Context, data []byte) ([]byte, error) {
	if len(data) >= len(backupEncryptMagic) && string(data[:len(backupEncryptMagic)]) == backupEncryptMagic {
		if c.localAEAD == nil {
			return nil, errors.New("archive encrypted with local key but BACKUP_ENCRYPT_KEY is not set")
		}
		rest := data[len(backupEncryptMagic):]
		if len(rest) < c.localAEAD.NonceSize() {
			return nil, errors.New("encrypted archive truncated")
		}
		nonce := rest[:c.localAEAD.NonceSize()]
		sealed := rest[c.localAEAD.NonceSize():]
		return c.localAEAD.Open(nil, nonce, sealed, []byte(backupEncryptMagic))
	}
	if c.encClient != nil {
		resp, err := c.encClient.Decrypt(ctx, &encryptionv1.DecryptRequest{Ciphertext: data})
		if err != nil {
			return nil, fmt.Errorf("encryption decrypt: %w", err)
		}
		return resp.GetPlaintext(), nil
	}
	if c.localAEAD != nil {
		return nil, errors.New("archive is not locally encrypted")
	}
	return data, nil
}

func decryptArchiveToTemp(ctx context.Context, c *archiveCryptor, archivePath string) (string, func(), error) {
	if c == nil || !c.enabled() {
		return archivePath, func() {}, nil
	}
	f, err := os.CreateTemp(filepath.Dir(archivePath), "backup-decrypt-*.tar.gz")
	if err != nil {
		return "", nil, err
	}
	_ = f.Close()
	if err := c.decryptFile(ctx, archivePath, f.Name()); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}
