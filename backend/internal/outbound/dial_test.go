package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func ips(values ...string) []net.IP {
	result := make([]net.IP, 0, len(values))
	for _, value := range values {
		result = append(result, net.ParseIP(value))
	}
	return result
}

func TestInterleaveAddressFamiliesKeepsPreferredFirstThenAlternates(t *testing.T) {
	got := interleaveAddressFamilies(ips("2001:2::73", "2001:2::74", "198.18.0.122", "198.18.0.123"))
	want := []string{"2001:2::73", "198.18.0.122", "2001:2::74", "198.18.0.123"}
	for i, ip := range got {
		if ip.String() != want[i] {
			t.Fatalf("顺序 %v，期望 %v", got, want)
		}
	}
	if single := interleaveAddressFamilies(ips("203.0.113.9")); len(single) != 1 {
		t.Fatalf("单个地址应原样返回：%v", single)
	}
}

func TestDialPinnedAddressesFallsBackWhenFirstAddressHangs(t *testing.T) {
	var hungCanceled atomic.Bool
	dial := func(ctx context.Context, _, address string) (net.Conn, error) {
		if host, _, _ := net.SplitHostPort(address); host == "2001:2::73" {
			// 模拟 IPv6 fake-ip：永远不通，直到被取消。
			<-ctx.Done()
			hungCanceled.Store(true)
			return nil, ctx.Err()
		}
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return client, nil
	}
	started := time.Now()
	conn, err := dialPinnedAddresses(context.Background(), dial, "tcp", ips("2001:2::73", "198.18.0.122"), "443")
	if err != nil {
		t.Fatalf("应回退到可用的 IPv4：%v", err)
	}
	defer conn.Close()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("回退耗时 %v，不应等到拨号超时", elapsed)
	}
	deadline := time.Now().Add(time.Second)
	for !hungCanceled.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !hungCanceled.Load() {
		t.Fatal("连上之后应取消仍在等待的首选地址")
	}
}

func TestDialPinnedAddressesTriesNextImmediatelyAfterFailure(t *testing.T) {
	var attempts []string
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		attempts = append(attempts, host)
		if host == "2001:2::73" {
			return nil, errors.New("no route to host")
		}
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return client, nil
	}
	started := time.Now()
	conn, err := dialPinnedAddresses(context.Background(), dial, "tcp", ips("2001:2::73", "198.18.0.122"), "443")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if time.Since(started) >= dialFallbackDelay {
		t.Fatalf("明确失败后应立刻试下一个，不等延迟：%v", time.Since(started))
	}
	if len(attempts) != 2 || attempts[1] != "198.18.0.122" {
		t.Fatalf("尝试顺序 %v", attempts)
	}
}

func TestDialPinnedAddressesReportsEveryFailureWhenAllFail(t *testing.T) {
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		return nil, errors.New("refused " + address)
	}
	_, err := dialPinnedAddresses(context.Background(), dial, "tcp", ips("2001:2::73", "198.18.0.122"), "443")
	if err == nil {
		t.Fatal("全部失败应返回错误")
	}
	for _, want := range []string{"2001:2::73", "198.18.0.122"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误应包含每个地址的失败原因 %s：%v", want, err)
		}
	}
	if _, err := dialPinnedAddresses(context.Background(), dial, "tcp", nil, "443"); err == nil {
		t.Fatal("没有地址应返回错误")
	}
}

func TestDialPinnedAddressesStopsWhenContextIsCanceled(t *testing.T) {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := dialPinnedAddresses(ctx, dial, "tcp", ips("2001:2::73", "198.18.0.122"), "443")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("上下文超时应及时返回：%v，用时 %v", err, time.Since(started))
	}
}

func TestOutboundTransportFallsBackToReachableAddress(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	// 127.0.0.2 上没有监听（拒绝连接），127.0.0.1 才有：首选地址不通时必须回退。
	transport := newOutboundTransport(func(context.Context, string) ([]net.IP, error) { return ips("127.0.0.2", "127.0.0.1"), nil })
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("http://upstream.example:" + target.Port() + "/")
	if err != nil {
		t.Fatalf("经 Transport 应回退到可用地址：%v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", response.StatusCode)
	}
}
