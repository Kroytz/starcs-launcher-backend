package passwordauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	ctx := context.Background()
	encoded, err := Hash(ctx, "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected encoded hash: %q", encoded)
	}
	valid, err := Verify(ctx, "correct-horse-battery-staple", encoded)
	if err != nil || !valid {
		t.Fatalf("verify password: valid=%v err=%v", valid, err)
	}
	valid, err = Verify(ctx, "incorrect-password", encoded)
	if err != nil || valid {
		t.Fatalf("wrong password should fail: valid=%v err=%v", valid, err)
	}
}

func TestHashUsesUniqueSalt(t *testing.T) {
	ctx := context.Background()
	first, err := Hash(ctx, "same-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Hash(ctx, "same-password")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("password hashes must use unique salts")
	}
}

func TestPasswordValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := Hash(ctx, "short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("expected short password error, got %v", err)
	}
	if _, err := Hash(ctx, strings.Repeat("a", MaxPasswordBytes+1)); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("expected long password error, got %v", err)
	}
	if _, err := Verify(ctx, "password", "not-a-phc-hash"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("expected invalid hash error, got %v", err)
	}
}

func TestVerifyReturnsBusyWhenSaturated(t *testing.T) {
	ctx := context.Background()
	encoded, err := Hash(ctx, "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(argon2Gate); i++ {
		argon2Gate <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(argon2Gate); i++ {
			<-argon2Gate
		}
	}()

	busyCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = Verify(busyCtx, "correct-horse-battery-staple", encoded)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
}
