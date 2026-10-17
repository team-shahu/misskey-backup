//go:build e2e

package e2e

import (
	"context"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	accessKey = "e2e-access-key"
	secretKey = "e2e-secret-key"
	region    = "us-east-1"
	bucket    = "e2e"
	prefix    = "backups"
)

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testcontainers.CleanupNetwork(t, nw)

	pg, err := testcontainers.Run(ctx, "groonga/pgroonga:latest-debian-17",
		network.WithNetwork([]string{"postgres"}, nw),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_DB":       "mk1",
			"POSTGRES_USER":     "postgres",
			"POSTGRES_PASSWORD": "testpassword",
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      "../test-data.sql",
			ContainerFilePath: "/docker-entrypoint-initdb.d/test-data.sql",
			FileMode:          0o644,
		}),
		// initdb後の再起動分含め2回
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	testcontainers.CleanupContainer(t, pg)
	if err != nil {
		t.Fatal(err)
	}

	redis, err := testcontainers.Run(ctx, "redis:7-alpine",
		network.WithNetwork([]string{"redis"}, nw),
		testcontainers.WithWaitStrategy(wait.ForLog("Ready to accept connections")),
	)
	testcontainers.CleanupContainer(t, redis)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, redis, "redis-cli", "set", "e2e:key", "value")

	rustfs, err := testcontainers.Run(ctx, "rustfs/rustfs:1.0.3",
		network.WithNetwork([]string{"s3"}, nw),
		testcontainers.WithEnv(map[string]string{
			"RUSTFS_ACCESS_KEY": accessKey,
			"RUSTFS_SECRET_KEY": secretKey,
		}),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("9000/tcp")),
	)
	testcontainers.CleanupContainer(t, rustfs)
	if err != nil {
		t.Fatal(err)
	}

	s3Endpoint, err := rustfs.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	client := newS3Client(s3Endpoint)
	createBucket(ctx, t, client)

	app, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{Context: ".."}),
		network.WithNetwork([]string{"backup"}, nw),
		testcontainers.WithEnv(map[string]string{
			"TZ":                       "Asia/Tokyo",
			"POSTGRES_HOST":            "postgres",
			"POSTGRES_USER":            "postgres",
			"POSTGRES_PASSWORD":        "testpassword",
			"POSTGRES_DB":              "mk1",
			"REDIS_HOST":               "redis",
			"BACKUP_ENCRYPTION_KEY":    "e2e-encryption-key-0123456789abcdef",
			"BACKUP_ENDPOINT":          "http://s3:9000",
			"BACKUP_ACCESS_KEY_ID":     accessKey,
			"BACKUP_SECRET_ACCESS_KEY": secretKey,
			"BACKUP_REGION":            region,
			"BACKUP_FORCE_PATH_STYLE":  "true",
			"R2_BUCKET_NAME":           bucket,
			"R2_PREFIX":                prefix,
			"MAX_RETRIES":              "1",
			// 起動直後の初回バックアップ実行用
			"CRON_SCHEDULE": "0 0 1 1 *",
		}),
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(5*time.Minute,
			wait.ForLog("Scheduled backup finished"),
			wait.ForHTTP("/health").WithPort("8080/tcp"),
		),
	)
	testcontainers.CleanupContainer(t, app)
	if err != nil {
		t.Fatal(err)
	}

	logs := containerLogs(ctx, t, app)
	for _, target := range []string{"PostgreSQL", "Redis"} {
		if !strings.Contains(logs, target+" backup completed successfully") {
			t.Fatalf("%s backup did not succeed:\n%s", target, logs)
		}
	}

	pgObject := findObject(ctx, t, client, regexp.MustCompile(`^mk1_[0-9_-]+\.dump\.zst\.enc$`))
	redisObject := findObject(ctx, t, client, regexp.MustCompile(`^redis_[0-9_-]+\.rdb\.zst\.enc$`))

	// アプリコンテナから到達可能なホスト名で署名
	presignClient := s3.NewPresignClient(newS3Client("http://s3:9000"))

	t.Run("restore PostgreSQL", func(t *testing.T) {
		dump := restore(ctx, t, app, presignClient, pgObject)
		toc := mustExec(ctx, t, app, "pg_restore", "--list", dump)
		if !strings.Contains(toc, "TABLE public users") {
			t.Errorf("restored dump does not contain users table:\n%s", toc)
		}
	})

	t.Run("restore Redis", func(t *testing.T) {
		rdb := restore(ctx, t, app, presignClient, redisObject)
		mustExec(ctx, t, app, "redis-check-rdb", rdb)
	})
}

func newS3Client(endpoint string) *s3.Client {
	return s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		UsePathStyle: true,
	})
}

// RustFS起動直後の失敗分リトライ
func createBucket(ctx context.Context, t *testing.T, client *s3.Client) {
	t.Helper()
	var err error
	for range 30 {
		if _, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("failed to create bucket: %v", err)
}

func findObject(ctx context.Context, t *testing.T, client *s3.Client, re *regexp.Regexp) string {
	t.Helper()
	out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix + "/"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, obj := range out.Contents {
		name := strings.TrimPrefix(*obj.Key, prefix+"/")
		if re.MatchString(name) {
			return name
		}
		keys = append(keys, *obj.Key)
	}
	t.Fatalf("no object matches %s: %v", re, keys)
	return ""
}

// 署名付きURLから復元し復元先パスを返す
func restore(ctx context.Context, t *testing.T, app testcontainers.Container, presignClient *s3.PresignClient, object string) string {
	t.Helper()
	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(prefix + "/" + object),
	}, s3.WithPresignExpires(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, app, "./misskey-backup", "--restore-url", req.URL)
	return "restore/" + strings.TrimSuffix(object, ".zst.enc")
}

func mustExec(ctx context.Context, t *testing.T, c testcontainers.Container, cmd ...string) string {
	t.Helper()
	code, r, err := c.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("%v exited with %d:\n%s", cmd, code, out)
	}
	return string(out)
}

func containerLogs(ctx context.Context, t *testing.T, c testcontainers.Container) string {
	t.Helper()
	r, err := c.Logs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
