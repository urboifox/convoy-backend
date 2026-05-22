package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port            string
	DatabaseURL     string
	JWTSecret       []byte
	JWTTTL          time.Duration
	AllowedOrigins  []string
	WSWriteTimeout  time.Duration
	WSReadTimeout   time.Duration
	WSPingInterval  time.Duration
	LiveKitURL      string
	LiveKitAPIKey   string
	LiveKitAPISecret string
	// Admin dashboard. Both must be set to enable /admin. CookieSecret signs
	// the session cookie; rotate it to invalidate every existing session.
	AdminPassword     string
	AdminCookieSecret []byte
	// Optional Expo Push access token; required only for projects whose Expo
	// account enforces "Enhanced Push Security".
	ExpoAccessToken string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	c := &Config{
		Port:           getenv("PORT", "8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		JWTSecret:      []byte(os.Getenv("JWT_SECRET")),
		JWTTTL:         getDuration("JWT_TTL", 30*24*time.Hour),
		AllowedOrigins: []string{"*"},
		WSWriteTimeout: getDuration("WS_WRITE_TIMEOUT", 10*time.Second),
		WSReadTimeout:  getDuration("WS_READ_TIMEOUT", 60*time.Second),
		WSPingInterval: getDuration("WS_PING_INTERVAL", 25*time.Second),
		LiveKitURL:      os.Getenv("LIVEKIT_URL"),
		LiveKitAPIKey:   os.Getenv("LIVEKIT_API_KEY"),
		LiveKitAPISecret: os.Getenv("LIVEKIT_API_SECRET"),
		AdminPassword:     os.Getenv("ADMIN_PASSWORD"),
		AdminCookieSecret: []byte(os.Getenv("ADMIN_COOKIE_SECRET")),
		ExpoAccessToken:   os.Getenv("EXPO_ACCESS_TOKEN"),
	}

	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 16 {
		return nil, errors.New("JWT_SECRET must be at least 16 characters")
	}
	return c, nil
}

func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func getDuration(k string, fallback time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return fallback
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return fallback
}
