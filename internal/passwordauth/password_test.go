package passwordauth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	encoded, err := Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected encoded hash: %q", encoded)
	}
	valid, err := Verify("correct-horse-battery-staple", encoded)
	if err != nil || !valid {
		t.Fatalf("verify password: valid=%v err=%v", valid, err)
	}
	valid, err = Verify("incorrect-password", encoded)
	if err != nil || valid {
		t.Fatalf("wrong password should fail: valid=%v err=%v", valid, err)
	}
}

func TestHashUsesUniqueSalt(t *testing.T) {
	first, err := Hash("same-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Hash("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("password hashes must use unique salts")
	}
}

func TestPasswordValidation(t *testing.T) {
	if _, err := Hash("short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("expected short password error, got %v", err)
	}
	if _, err := Hash(strings.Repeat("a", MaxPasswordBytes+1)); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("expected long password error, got %v", err)
	}
	if _, err := Verify("password", "not-a-phc-hash"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("expected invalid hash error, got %v", err)
	}
}
