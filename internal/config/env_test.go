package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestLoadFilePreservesAtInDSN(t *testing.T) {
	const key = "STAR_TEST_DSN_WITH_AT"
	const dsn = "reader:pa@ss@tcp(mysql.example.com:3306)/db_star?parseTime=true"
	os.Unsetenv(key)
	t.Cleanup(func() { os.Unsetenv(key) })

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(key+"="+dsn+"\n"), 0o600); err != nil {
		t.Fatalf("write test env: %v", err)
	}
	if err := loadFile(path); err != nil {
		t.Fatalf("load env: %v", err)
	}
	if got := os.Getenv(key); got != dsn {
		t.Fatalf("expected DSN to remain unchanged, got %q", got)
	}
}

func TestDatabaseDSNHandlesAtInSeparatedPassword(t *testing.T) {
	t.Setenv("STAR_DB_DSN", "")
	t.Setenv("STAR_DB_USER", "reader")
	t.Setenv("STAR_DB_PASSWORD", "part-one@part-two@E!82")
	t.Setenv("STAR_DB_HOST", "mysql.example.com")
	t.Setenv("STAR_DB_PORT", "3306")
	t.Setenv("STAR_DB_NAME", "db_star")

	dsn, configured, err := DatabaseDSN()
	if err != nil {
		t.Fatalf("build DSN: %v", err)
	}
	if !configured {
		t.Fatal("expected database configuration")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse generated DSN: %v", err)
	}
	if parsed.Passwd != "part-one@part-two@E!82" {
		t.Fatal("generated DSN did not preserve password")
	}
	if parsed.Net != "tcp" || parsed.Addr != "mysql.example.com:3306" || parsed.DBName != "db_star" {
		t.Fatalf("unexpected generated database target: net=%q addr=%q db=%q", parsed.Net, parsed.Addr, parsed.DBName)
	}
}

func TestChallengeDatabaseDSNIsOptional(t *testing.T) {
	t.Setenv("STAR_DB_CHALLENGE_DSN", "")
	if _, configured, err := ChallengeDatabaseDSN(); err != nil || configured {
		t.Fatalf("expected optional challenge database to be disabled, configured=%v err=%v", configured, err)
	}
}

func TestChallengeDatabaseDSNPreservesPassword(t *testing.T) {
	t.Setenv("STAR_DB_CHALLENGE_DSN", "reader:pa@ss@tcp(mysql.example.com:3306)/db_challenge?parseTime=true")
	dsn, configured, err := ChallengeDatabaseDSN()
	if err != nil || !configured {
		t.Fatalf("expected challenge database configuration, configured=%v err=%v", configured, err)
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse challenge DSN: %v", err)
	}
	if parsed.Passwd != "pa@ss" || parsed.DBName != "db_challenge" {
		t.Fatal("challenge DSN did not preserve its credentials or database")
	}
}

func TestChallengeImportDSNDoesNotFallBackToReadOnlyDSN(t *testing.T) {
	t.Setenv("STAR_DB_CHALLENGE_DSN", "reader:secret@tcp(mysql.example.com:3306)/db_challenge")
	t.Setenv("STAR_DB_CHALLENGE_IMPORT_DSN", "")
	if _, configured, err := ChallengeImportDSN(); err != nil || configured {
		t.Fatalf("importer must require an explicit write DSN, configured=%v err=%v", configured, err)
	}
}

func TestLoadFileDoesNotOverrideProcessEnvironment(t *testing.T) {
	const key = "STAR_TEST_ENV_OVERRIDE"
	t.Setenv(key, "from-process")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(key+"=from-file\n"), 0o600); err != nil {
		t.Fatalf("write test env: %v", err)
	}
	if err := loadFile(path); err != nil {
		t.Fatalf("load env: %v", err)
	}
	if got := os.Getenv(key); got != "from-process" {
		t.Fatalf("expected process environment to win, got %q", got)
	}
}

func TestSkipPasswordAuthDefaultsToDisabled(t *testing.T) {
	t.Setenv("STAR_SKIP_PASSWORD_AUTH", "")
	enabled, err := SkipPasswordAuth()
	if err != nil {
		t.Fatalf("read password auth setting: %v", err)
	}
	if enabled {
		t.Fatal("password auth bypass must be disabled by default")
	}
}

func TestSkipPasswordAuthRequiresExplicitBoolean(t *testing.T) {
	t.Setenv("STAR_SKIP_PASSWORD_AUTH", "true")
	enabled, err := SkipPasswordAuth()
	if err != nil {
		t.Fatalf("read password auth setting: %v", err)
	}
	if !enabled {
		t.Fatal("expected password auth bypass to be enabled")
	}

	t.Setenv("STAR_SKIP_PASSWORD_AUTH", "sometimes")
	if _, err := SkipPasswordAuth(); err == nil {
		t.Fatal("invalid boolean setting should fail startup")
	}
}
