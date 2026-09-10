// Package secretbox seals stored credentials.
//
// Envelope encryption: each value is sealed with its own data key, and the data
// key is sealed with a master key supplied by the operator's environment. The
// structure is what matters - moving the master key into a KMS later replaces
// this component rather than the schema or the callers.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// MasterKeyEnvVar is where the operator puts the master key.
const MasterKeyEnvVar = "TASKD_SECRET_KEY"

const keyLength = 32

// ErrNoMasterKey is returned when the installation has no master key.
var ErrNoMasterKey = errors.New("no master key configured")

// Sealer seals and opens stored values.
type Sealer struct {
	masterKey []byte
}

// NewFromEnv reads the master key from the environment.
//
// The key is required rather than defaulted: a fallback would mean every
// installation that never set one shares the same key, which is the same as
// storing the credentials in the clear while appearing not to.
func NewFromEnv() (*Sealer, error) {
	encoded := os.Getenv(MasterKeyEnvVar)
	if encoded == "" {
		return nil, fmt.Errorf("%w: set %s to a base64-encoded 32-byte key "+
			"(openssl rand -base64 32)", ErrNoMasterKey, MasterKeyEnvVar)
	}
	return New(encoded)
}

// New builds a sealer from a base64-encoded master key.
func New(encodedMasterKey string) (*Sealer, error) {
	masterKey, err := base64.StdEncoding.DecodeString(encodedMasterKey)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", MasterKeyEnvVar, err)
	}
	if len(masterKey) != keyLength {
		return nil, fmt.Errorf("%s must decode to %d bytes, got %d",
			MasterKeyEnvVar, keyLength, len(masterKey))
	}
	return &Sealer{masterKey: masterKey}, nil
}

// GenerateMasterKey produces a key in the form the environment variable expects.
func GenerateMasterKey() (string, error) {
	key := make([]byte, keyLength)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// Seal encrypts a value, returning the sealed value and its sealed data key.
func (s *Sealer) Seal(plaintext string) (sealedValue, sealedDataKey []byte, err error) {
	dataKey := make([]byte, keyLength)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		return nil, nil, err
	}

	sealedValue, err = encrypt(dataKey, []byte(plaintext))
	if err != nil {
		return nil, nil, err
	}
	sealedDataKey, err = encrypt(s.masterKey, dataKey)
	if err != nil {
		return nil, nil, err
	}
	return sealedValue, sealedDataKey, nil
}

// Open decrypts a value sealed by Seal.
func (s *Sealer) Open(sealedValue, sealedDataKey []byte) (string, error) {
	dataKey, err := decrypt(s.masterKey, sealedDataKey)
	if err != nil {
		return "", fmt.Errorf("could not unseal the data key: %w", err)
	}

	plaintext, err := decrypt(dataKey, sealedValue)
	if err != nil {
		return "", fmt.Errorf("could not unseal the value: %w", err)
	}
	return string(plaintext), nil
}

func encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// The nonce is prefixed to the ciphertext, so a sealed value is one blob.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(key, sealed []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("the sealed value is too short")
	}

	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Mask renders a credential the way the dashboard shows it: enough to tell which
// key is stored, never enough to use it.
func Mask(value string) string {
	const visible = 4
	if len(value) <= visible {
		return "…"
	}
	return "…" + value[len(value)-visible:]
}
