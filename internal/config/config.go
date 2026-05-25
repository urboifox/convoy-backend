package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
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

	// OAuth audiences. Each is a comma-separated list of accepted client
	// ids. Leave empty to disable the corresponding sign-in method.
	GoogleClientIDs []string
	AppleClientIDs  []string

	// Privacy-policy template values surfaced at /policy. ContactEmail is
	// required for the page to render — when missing, the route is wired
	// off and the mobile settings screen hides the link.
	PrivacyContactEmail  string
	PrivacyEffectiveDate string
	// AppName is shown in the policy header (defaults to "Convoy").
	AppName string
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

		GoogleClientIDs:      splitCSV(os.Getenv("GOOGLE_CLIENT_IDS")),
		AppleClientIDs:       splitCSV(os.Getenv("APPLE_CLIENT_IDS")),
		PrivacyContactEmail:  os.Getenv("PRIVACY_CONTACT_EMAIL"),
		PrivacyEffectiveDate: getenv("PRIVACY_EFFECTIVE_DATE", time.Now().Format("January 2, 2006")),
		AppName:              getenv("APP_NAME", "Convoy"),
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

// splitCSV trims whitespace and drops empty entries from a comma-separated
// env var. Returns nil for empty input so caller can `len()==0` test.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
