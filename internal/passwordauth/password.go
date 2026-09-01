package passwordauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

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

	// Cap concurrent Argon2 work so ~19 MiB allocations cannot exhaust process memory.
	maxConcurrentArgon2 = 4
	argon2AcquireWait   = 3 * time.Second
)

var (
	ErrPasswordTooShort = errors.New("password is too short")
	ErrPasswordTooLong  = errors.New("password is too long")
	ErrInvalidHash      = errors.New("invalid password hash")
	ErrBusy             = errors.New("password hashing is temporarily busy")

	argon2Gate = make(chan struct{}, maxConcurrentArgon2)
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

func Hash(ctx context.Context, password string) (string, error) {
	if err := Validate(password); err != nil {
		return "", err
	}
	if err := acquireArgon2(ctx); err != nil {
		return "", err
	}
	defer releaseArgon2()

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

func Verify(ctx context.Context, password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil ||
		memory == 0 || memory > maxMemoryKiB || timeCost == 0 || timeCost > maxIterations || threads == 0 || threads > maxParallelism {
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

	if err := acquireArgon2(ctx); err != nil {
		return false, err
	}
	defer releaseArgon2()

	actual := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1, nil
}

func acquireArgon2(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(argon2AcquireWait)
	defer timer.Stop()
	select {
	case argon2Gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	case <-timer.C:
		return ErrBusy
	}
}

func releaseArgon2() {
	<-argon2Gate
}
