//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	misskeyImage  = "misskey/misskey:2026.10.0"
	postgresImage = "postgres:18-alpine"
	redisImage    = "redis:7-alpine"

	dbName     = "misskey"
	dbUser     = "misskey"
	dbPassword = "misskey"
)

const misskeyConfig = `url: http://misskey:3000/
port: 3000
db:
  host: postgres
  port: 5432
  db: misskey
  user: misskey
  pass: misskey
dbReplications: false
redis:
  host: redis
  port: 6379
fulltextSearch:
  provider: sqlLike
id: 'aidx'
`

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()

	nw := newNetwork(ctx, t)
	startPostgres(ctx, t, nw)
	startRedis(ctx, t, nw)
	misskey := startMisskey(ctx, t, nw)

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

	// バックアップ対象データ投入
	misskeyURL := endpoint(ctx, t, misskey, "3000/tcp")
	var account struct {
		Token string `json:"token"`
	}
	callAPI(ctx, t, misskeyURL, "admin/accounts/create", map[string]any{
		"username": "e2e",
		"password": "e2e-password",
	}, &account)
	noteText := fmt.Sprintf("e2e note %d", time.Now().UnixNano())
	var created struct {
		CreatedNote struct {
			ID string `json:"id"`
		} `json:"createdNote"`
	}
	callAPI(ctx, t, misskeyURL, "notes/create", map[string]any{
		"i":    account.Token,
		"text": noteText,
	}, &created)
	noteID := created.CreatedNote.ID

	app, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{Context: ".."}),
		network.WithNetwork([]string{"backup"}, nw),
		testcontainers.WithEnv(map[string]string{
			"TZ":                       "Asia/Tokyo",
			"POSTGRES_HOST":            "postgres",
			"POSTGRES_USER":            dbUser,
			"POSTGRES_PASSWORD":        dbPassword,
			"POSTGRES_DB":              dbName,
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

	pgObject := findObject(ctx, t, client, regexp.MustCompile(`^misskey_[0-9_-]+\.dump\.zst\.enc$`))
	redisObject := findObject(ctx, t, client, regexp.MustCompile(`^redis_[0-9_-]+\.rdb\.zst\.enc$`))

	// アプリコンテナから到達可能なホスト名で署名
	presignClient := s3.NewPresignClient(newS3Client("http://s3:9000"))
	dump := readFile(ctx, t, app, restore(ctx, t, app, presignClient, pgObject))
	rdbPath := restore(ctx, t, app, presignClient, redisObject)
	mustExec(ctx, t, app, "redis-check-rdb", rdbPath)
	rdb := readFile(ctx, t, app, rdbPath)

	// 復元先は別ネットワーク上の同名ホスト
	restoredNw := newNetwork(ctx, t)
	restoredPg := startPostgres(ctx, t, restoredNw)
	if err := restoredPg.CopyToContainer(ctx, dump, "/tmp/backup.dump", 0o644); err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, restoredPg, "pg_restore", "--exit-on-error", "-U", dbUser, "-d", dbName, "/tmp/backup.dump")

	got := strings.TrimSpace(mustExec(ctx, t, restoredPg, "psql", "-U", dbUser, "-d", dbName, "-tAc",
		fmt.Sprintf("SELECT text FROM note WHERE id = '%s'", noteID)))
	if got != noteText {
		t.Fatalf("restored note text = %q, want %q", got, noteText)
	}

	startRedis(ctx, t, restoredNw, testcontainers.WithFiles(testcontainers.ContainerFile{
		Reader:            bytes.NewReader(rdb),
		ContainerFilePath: "/data/dump.rdb",
		FileMode:          0o644,
	}))
	restoredMisskey := startMisskey(ctx, t, restoredNw)
	restoredURL := endpoint(ctx, t, restoredMisskey, "3000/tcp")

	var me struct {
		Username string `json:"username"`
	}
	callAPI(ctx, t, restoredURL, "i", map[string]any{"i": account.Token}, &me)
	if me.Username != "e2e" {
		t.Errorf("restored username = %q, want %q", me.Username, "e2e")
	}

	var note struct {
		Text string `json:"text"`
	}
	callAPI(ctx, t, restoredURL, "notes/show", map[string]any{"noteId": noteID}, &note)
	if note.Text != noteText {
		t.Errorf("restored note text via API = %q, want %q", note.Text, noteText)
	}
}

func newNetwork(ctx context.Context, t *testing.T) *testcontainers.DockerNetwork {
	t.Helper()
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testcontainers.CleanupNetwork(t, nw)
	return nw
}

func startPostgres(ctx context.Context, t *testing.T, nw *testcontainers.DockerNetwork) *testcontainers.DockerContainer {
	t.Helper()
	c, err := testcontainers.Run(ctx, postgresImage,
		network.WithNetwork([]string{"postgres"}, nw),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_DB":       dbName,
			"POSTGRES_USER":     dbUser,
			"POSTGRES_PASSWORD": dbPassword,
		}),
		// initdb後の再起動分含め2回
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func startRedis(ctx context.Context, t *testing.T, nw *testcontainers.DockerNetwork, opts ...testcontainers.ContainerCustomizer) *testcontainers.DockerContainer {
	t.Helper()
	opts = append([]testcontainers.ContainerCustomizer{
		network.WithNetwork([]string{"redis"}, nw),
		testcontainers.WithWaitStrategy(wait.ForLog("Ready to accept connections")),
	}, opts...)
	c, err := testcontainers.Run(ctx, redisImage, opts...)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func startMisskey(ctx context.Context, t *testing.T, nw *testcontainers.DockerNetwork) *testcontainers.DockerContainer {
	t.Helper()
	c, err := testcontainers.Run(ctx, misskeyImage,
		network.WithNetwork([]string{"misskey"}, nw),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(misskeyConfig),
			ContainerFilePath: "/misskey/.config/default.yml",
			FileMode:          0o644,
		}),
		testcontainers.WithExposedPorts("3000/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(10*time.Minute,
			wait.ForHTTP("/api/meta").WithPort("3000/tcp").
				WithMethod(http.MethodPost).
				WithHeaders(map[string]string{"Content-Type": "application/json"}).
				WithBody(strings.NewReader("{}")),
		),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func endpoint(ctx context.Context, t *testing.T, c *testcontainers.DockerContainer, port string) string {
	t.Helper()
	u, err := c.PortEndpoint(ctx, port, "http")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func callAPI(ctx context.Context, t *testing.T, baseURL, name string, body map[string]any, out any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/"+name, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s returned %s: %s", name, resp.Status, respBody)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		t.Fatalf("%s: failed to decode response: %v", name, err)
	}
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
	return "/app/restore/" + strings.TrimSuffix(object, ".zst.enc")
}

func readFile(ctx context.Context, t *testing.T, c testcontainers.Container, path string) []byte {
	t.Helper()
	r, err := c.CopyFileFromContainer(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
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
