package config

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// LoadDotEnv loads the first available .env file without replacing variables
// that were explicitly supplied by the parent process.
func LoadDotEnv() (string, error) {
	candidates := []string{".env"}
	if executable, err := os.Executable(); err == nil {
		executableDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(executableDir, ".env"),
			filepath.Join(executableDir, "..", ".env"),
		)
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, exists := seen[absolute]; exists {
			continue
		}
		seen[absolute] = struct{}{}
		if err := loadFile(absolute); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		return absolute, nil
	}
	return "", nil
}

// DatabaseDSN returns a validated driver DSN. Separate fields are recommended
// because mysql.Config.FormatDSN safely handles passwords containing @ or other
// DSN punctuation.
func DatabaseDSN() (string, bool, error) {
	if raw := strings.TrimSpace(os.Getenv("STAR_DB_DSN")); raw != "" {
		parsed, err := mysql.ParseDSN(raw)
		if err != nil {
			return "", false, fmt.Errorf("parse STAR_DB_DSN: %w", err)
		}
		if parsed.Net != "tcp" {
			return "", false, errors.New("STAR_DB_DSN must use username:password@tcp(host:port)/database; @ inside the password does not need escaping")
		}
		return parsed.FormatDSN(), true, nil
	}

	user := strings.TrimSpace(os.Getenv("STAR_DB_USER"))
	password, hasPassword := os.LookupEnv("STAR_DB_PASSWORD")
	host := strings.TrimSpace(os.Getenv("STAR_DB_HOST"))
	port := strings.TrimSpace(os.Getenv("STAR_DB_PORT"))
	database := strings.TrimSpace(os.Getenv("STAR_DB_NAME"))
	configured := user != "" || hasPassword || host != "" || port != "" || database != ""
	if !configured {
		return "", false, nil
	}
	if user == "" || !hasPassword || host == "" || database == "" {
		return "", false, errors.New("STAR_DB_USER, STAR_DB_PASSWORD, STAR_DB_HOST and STAR_DB_NAME must all be configured")
	}
	if port == "" {
		port = "3306"
	}

	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, port)
	cfg.DBName = database
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 8 * time.Second
	cfg.WriteTimeout = 8 * time.Second
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	return cfg.FormatDSN(), true, nil
}

// SkipPasswordAuth enables the temporary Steam64-only read session mode.
// It is disabled by default and must be explicitly enabled by configuration.
func SkipPasswordAuth() (bool, error) {
	raw := strings.TrimSpace(os.Getenv("STAR_SKIP_PASSWORD_AUTH"))
	if raw == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("parse STAR_SKIP_PASSWORD_AUTH: %w", err)
	}
	return enabled, nil
}

func loadFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || !validKey(key) {
			return fmt.Errorf("%s:%d contains an invalid environment assignment", path, lineNumber)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from %s: %w", key, path, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for index, character := range key {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			character == '_' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}
