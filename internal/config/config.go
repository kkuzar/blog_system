package config

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type ServerConfig struct {
	Port string
	Host string
}

type JWTConfig struct {
	Secret     string
	Expiration time.Duration
}

type DBConfig struct {
	Type         string // "mongodb", "dynamodb", "firestore"
	MongoURI     string
	MongoDBName  string
	DynamoRegion string
	DynamoTable  string
	// Dynamo credentials handled by AWS SDK (env vars, shared config, IAM role)
	FirestoreProjectID   string
	FirestoreCredentials string // Path to service account JSON file
}

type StorageConfig struct {
	Type           string // "s3"
	S3Region       string
	S3Bucket       string
	S3Endpoint     string // Optional: for MinIO or other S3 compatible
	S3AccessKey    string // Optional: Use IAM roles in production
	S3SecretKey    string // Optional: Use IAM roles in production
	S3UsePathStyle bool   // Optional: for MinIO
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	Enabled  bool // Flag to enable/disable caching easily
}

type SnapshotConfig struct {
	IntervalChanges int // Take snapshot every N changes (0 to disable)
}

type Config struct {
	Server   ServerConfig
	JWT      JWTConfig
	Database DBConfig
	Storage  StorageConfig
	Redis    RedisConfig    // Added
	Snapshot SnapshotConfig // Added
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load()

	jwtExpMinutes, _ := strconv.Atoi(getEnv("JWT_EXPIRATION_MINUTES", "60"))
	s3UsePathStyle, _ := strconv.ParseBool(getEnv("S3_USE_PATH_STYLE", "false"))
	redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))
	redisEnabled, _ := strconv.ParseBool(getEnv("REDIS_ENABLED", "true"))          // Enabled by default if configured
	snapshotInterval, _ := strconv.Atoi(getEnv("SNAPSHOT_INTERVAL_CHANGES", "50")) // Snapshot every 50 changes

	cfg := &Config{
		Server: ServerConfig{
			Port: getEnv("SERVER_PORT", "8080"),
			Host: getEnv("SERVER_HOST", "localhost"),
		},
		JWT: JWTConfig{
			Secret:     getEnv("JWT_SECRET", "a_very_secret_key"),
			Expiration: time.Duration(jwtExpMinutes) * time.Minute,
		},
		Database: DBConfig{
			Type:                 getEnv("DB_TYPE", "mongodb"),
			MongoURI:             getEnv("MONGO_URI", ""),
			MongoDBName:          getEnv("MONGO_DB_NAME", ""),
			DynamoRegion:         getEnv("AWS_REGION", ""),
			DynamoTable:          getEnv("DYNAMO_TABLE_NAME", ""),
			FirestoreProjectID:   getEnv("FIRESTORE_PROJECT_ID", ""),
			FirestoreCredentials: getEnv("FIRESTORE_CREDENTIALS_FILE", ""),
		},
		Storage: StorageConfig{
			Type:           getEnv("STORAGE_TYPE", "s3"),
			S3Region:       getEnv("AWS_REGION", ""),
			S3Bucket:       getEnv("S3_BUCKET_NAME", ""),
			S3Endpoint:     getEnv("S3_ENDPOINT", ""),
			S3AccessKey:    getEnv("AWS_ACCESS_KEY_ID", ""),
			S3SecretKey:    getEnv("AWS_SECRET_ACCESS_KEY", ""),
			S3UsePathStyle: s3UsePathStyle,
		},
		Redis: RedisConfig{ // Added
			Enabled:  redisEnabled,
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       redisDB,
		},
		Snapshot: SnapshotConfig{ // Added
			IntervalChanges: snapshotInterval,
		},
	}

	// Basic validation
	if cfg.JWT.Secret == "a_very_secret_key" {
		log.Println("WARNING: JWT_SECRET is set to the default insecure value.")
	}
	if cfg.Storage.Type == "s3" && cfg.Storage.S3Bucket == "" {
		log.Println("WARNING: STORAGE_TYPE is s3 but S3_BUCKET_NAME is not set.")
	}
	if cfg.Redis.Enabled && cfg.Redis.Addr == "" {
		log.Println("WARNING: REDIS_ENABLED is true but REDIS_ADDR is not set. Disabling Redis.")
		cfg.Redis.Enabled = false
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
