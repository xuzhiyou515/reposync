package app

import "os"

type Config struct {
	HTTPAddr  string
	DBPath    string
	CacheDir  string
	SecretKey string
	GitBin    string
}

func LoadConfigFromEnv() Config {
	return Config{
		HTTPAddr:  getenv("REPOSYNC_HTTP_ADDR", ":8080"),
		DBPath:    getenv("REPOSYNC_DB_PATH", "data/reposync.db"),
		CacheDir:  getenv("REPOSYNC_CACHE_DIR", "data/cache"),
		SecretKey: getenv("REPOSYNC_SECRET_KEY", "reposync-dev-secret"),
		GitBin:    getenv("REPOSYNC_GIT_BIN", "git"),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
