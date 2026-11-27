// Package config reads settings from the environment.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr         string
	BaseURL      string
	DatabaseURL  string
	DataDir      string
	FileKey      []byte // 32 bytes, AES-256
	DevMode      bool
	CookieSecure bool

	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string

	// SMSWebhookURL receives POST {"to":..., "message":...} for OTP texts.
	SMSWebhookURL   string
	SMSWebhookToken string

	ExpoPushURL     string
	ExpoAccessToken string
	PushEnabled     bool
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes"
}

// Load reads the configuration. Missing secrets fall back to development values.
func Load() Config {
	c := Config{
		Addr:            env("ADDR", ":8080"),
		BaseURL:         strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		DatabaseURL:     env("DATABASE_URL", "postgres://eimsky:eimsky@localhost:5432/eimsky?sslmode=disable"),
		DataDir:         env("DATA_DIR", "./data"),
		DevMode:         envBool("DEV_MODE", true),
		SMTPHost:        env("SMTP_HOST", ""),
		SMTPUser:        env("SMTP_USER", ""),
		SMTPPass:        env("SMTP_PASS", ""),
		SMTPFrom:        env("SMTP_FROM", "Eimsky <no-reply@eimsky.local>"),
		SMSWebhookURL:   env("SMS_WEBHOOK_URL", ""),
		SMSWebhookToken: env("SMS_WEBHOOK_TOKEN", ""),
		ExpoPushURL:     env("EXPO_PUSH_URL", "https://exp.host/--/api/v2/push/send"),
		ExpoAccessToken: env("EXPO_ACCESS_TOKEN", ""),
		PushEnabled:     envBool("PUSH_ENABLED", true),
	}
	c.SMTPPort, _ = strconv.Atoi(env("SMTP_PORT", "587"))
	c.CookieSecure = envBool("COOKIE_SECURE", strings.HasPrefix(c.BaseURL, "https://"))
	if k := env("FILE_KEY", ""); k != "" {
		b, err := hex.DecodeString(k)
		if err != nil || len(b) != 32 {
			log.Fatal("FILE_KEY must be 64 hex characters (32 bytes)")
		}
		c.FileKey = b
	} else {
		if !c.DevMode {
			log.Fatal("FILE_KEY is required when DEV_MODE is off")
		}
		sum := sha256.Sum256([]byte("eimsky-development-file-key"))
		c.FileKey = sum[:]
	}
	return c
}
