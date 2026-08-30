package passwordauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordBytes = 8
	MaxPasswordBytes = 128
	memoryKiB        = 19 * 1024
	iterations       = 2
	parallelism      = 1
	saltLength       = 16
	keyLength        = 32
	maxMemoryKiB     = 256 * 1024
	maxIterations    = 10
	maxParallelism   = 16
	maxSaltLength    = 64
	maxKeyLength     = 64
)

var (
	ErrPasswordTooShort = errors.New("password is too short")
	ErrPasswordTooLong  = errors.New("password is too long")
	ErrInvalidHash      = errors.New("invalid password hash")
)

func Validate(password string) error {
	length := len([]byte(password))
	if length < MinPasswordBytes {
		return ErrPasswordTooShort
	}
	if length > MaxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

func Hash(password string) (string, error) {
	if err := Validate(password); err != nil {
		return "", err
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, iterations, memoryKiB, parallelism, keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memoryKiB,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil ||
		memory == 0 || memory > maxMemoryKiB || time == 0 || time > maxIterations || threads == 0 || threads > maxParallelism {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > maxSaltLength {
		return false, ErrInvalidHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > maxKeyLength {
		return false, ErrInvalidHash
	}
	actual := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1, nil
}
