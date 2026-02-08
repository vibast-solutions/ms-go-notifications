//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	types "github.com/vibast-solutions/ms-go-notifications/app/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	defaultHTTPBase = "http://localhost:8080"
	defaultGRPCAddr = "localhost:9090"
	defaultMySQLDSN = "root:root@tcp(localhost:3307)/notifications?parseTime=true"
)

type httpClient struct {
	baseURL string
	client  *http.Client
}

func newHTTPClient() *httpClient {
	base := os.Getenv("NOTIFICATIONS_HTTP_URL")
	if base == "" {
		base = defaultHTTPBase
	}
	return &httpClient{
		baseURL: base,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *httpClient) postJSON(t *testing.T, path string, body any) (*http.Response, []byte) {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		t.Fatalf("http request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := ioReadAll(resp)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	return resp, bodyBytes
}

func waitForHTTP(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/health", nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("http service not ready at %s", baseURL)
}

func waitForGRPC(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("grpc service not ready at %s", addr)
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NOTIFICATIONS_MYSQL_DSN")
	if dsn == "" {
		dsn = defaultMySQLDSN
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db failed: %v", err)
	}
	return db
}

func waitForStatus(t *testing.T, db *sql.DB, requestID string, status int16, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var got int16
		err := db.QueryRow("SELECT status FROM email_history WHERE request_id = ?", requestID).Scan(&got)
		if err == nil && got == status {
			return
		}
		if err != nil && err != sql.ErrNoRows {
			t.Fatalf("db query failed: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for status %d for request_id=%s", status, requestID)
}

func TestNotificationsE2E(t *testing.T) {
	httpBase := os.Getenv("NOTIFICATIONS_HTTP_URL")
	if httpBase == "" {
		httpBase = defaultHTTPBase
	}
	grpcAddr := os.Getenv("NOTIFICATIONS_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = defaultGRPCAddr
	}

	if err := waitForHTTP(httpBase, 30*time.Second); err != nil {
		t.Fatalf("http not ready: %v", err)
	}
	if err := waitForGRPC(grpcAddr, 30*time.Second); err != nil {
		t.Fatalf("grpc not ready: %v", err)
	}

	db := openDB(t)
	defer db.Close()

	client := newHTTPClient()

	t.Run("HTTPValidation", func(t *testing.T) {
		resp, _ := client.postJSON(t, "/email/send/raw", map[string]string{})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for missing fields, got %d", resp.StatusCode)
		}

		resp, _ = client.postJSON(t, "/email/send/raw", map[string]string{
			"request_id": "bad-email",
			"recipient":  "invalid",
			"subject":    "Hello",
			"content":    "hello world from e2e",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid email, got %d", resp.StatusCode)
		}

		resp, _ = client.postJSON(t, "/email/send/raw", map[string]string{
			"request_id": "short-subject",
			"recipient":  "e2e@example.com",
			"subject":    "abc",
			"content":    "hello world from e2e",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for short subject, got %d", resp.StatusCode)
		}

		resp, _ = client.postJSON(t, "/email/send/raw", map[string]string{
			"request_id": "short-content",
			"recipient":  "e2e@example.com",
			"subject":    "Hello",
			"content":    "short",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for short content, got %d", resp.StatusCode)
		}
	})

	t.Run("HTTPIdempotency", func(t *testing.T) {
		requestID := fmt.Sprintf("e2e-http-%d", time.Now().UnixNano())
		resp, body := client.postJSON(t, "/email/send/raw", map[string]string{
			"request_id": requestID,
			"recipient":  "e2e@example.com",
			"subject":    "Hello",
			"content":    "hello world from e2e",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("http send raw failed: %d body: %s", resp.StatusCode, string(body))
		}
		waitForStatus(t, db, requestID, entity.EmailStatusSuccess, 20*time.Second)

		resp, _ = client.postJSON(t, "/email/send/raw", map[string]string{
			"request_id": requestID,
			"recipient":  "e2e@example.com",
			"subject":    "Hello",
			"content":    "hello world from e2e",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for duplicate request_id, got %d", resp.StatusCode)
		}
	})

	conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial failed: %v", err)
	}
	defer conn.Close()

	grpcClient := types.NewNotificationsServiceClient(conn)

	t.Run("GRPCValidation", func(t *testing.T) {
		_, err = grpcClient.SendRawEmail(context.Background(), &types.SendRawEmailRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument, got %v", err)
		}
	})

	t.Run("GRPCIdempotency", func(t *testing.T) {
		grpcRequestID := fmt.Sprintf("e2e-grpc-%d", time.Now().UnixNano())
		_, err = grpcClient.SendRawEmail(context.Background(), &types.SendRawEmailRequest{
			RequestId: grpcRequestID,
			Recipient: "e2e@example.com",
			Subject:   "Hello",
			Content:   "hello world from grpc e2e",
		})
		if err != nil {
			t.Fatalf("grpc send raw failed: %v", err)
		}
		waitForStatus(t, db, grpcRequestID, entity.EmailStatusSuccess, 20*time.Second)

		_, err = grpcClient.SendRawEmail(context.Background(), &types.SendRawEmailRequest{
			RequestId: grpcRequestID,
			Recipient: "e2e@example.com",
			Subject:   "Hello",
			Content:   "hello world from grpc e2e",
		})
		if status.Code(err) != codes.AlreadyExists {
			t.Fatalf("expected AlreadyExists, got %v", err)
		}
	})
}

func ioReadAll(resp *http.Response) ([]byte, error) {
	buf := &bytes.Buffer{}
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}
