package outbound

import (
	"context"
	"errors"
	"net"
	"time"
)

// dialFallbackDelay 是首选地址迟迟不通时，开始尝试下一个地址的等待时间（RFC 8305 建议 250ms）。
const dialFallbackDelay = 300 * time.Millisecond

type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// dialPinnedAddresses 依次尝试已通过 SSRF 校验的地址，第一个连上的为准，其余取消。
//
// 调用方必须保证 addresses 里每一个都已经过 blockedOutboundIP 等校验：这里只在"校验过的集合"内
// 回退，不会重新解析域名，所以不会削弱防 DNS 重绑定的保护。
// 只拨第一个地址会在双栈主机上踩坑：IPv6 路由不通（没有 IPv6 出口，或 TUN/VPN 返回了不可达的
// IPv6 fake-ip）时要等满拨号超时，而 IPv4 其实是通的。
func dialPinnedAddresses(ctx context.Context, dial dialFunc, network string, addresses []net.IP, port string) (net.Conn, error) {
	ordered := interleaveAddressFamilies(addresses)
	if len(ordered) == 0 {
		return nil, errors.New("没有可用的上游地址")
	}
	if len(ordered) == 1 {
		return dial(ctx, network, net.JoinHostPort(ordered[0].String(), port))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		conn net.Conn
		err  error
	}
	outcomes := make(chan outcome, len(ordered))
	next, pending := 0, 0
	launch := func() {
		address := net.JoinHostPort(ordered[next].String(), port)
		next++
		pending++
		go func() {
			conn, err := dial(ctx, network, address)
			outcomes <- outcome{conn, err}
		}()
	}
	// 赢家之外已经连上的连接要关掉，否则泄漏。
	closeLosers := func(count int) {
		go func() {
			for i := 0; i < count; i++ {
				if late := <-outcomes; late.conn != nil {
					_ = late.conn.Close()
				}
			}
		}()
	}

	launch()
	timer := time.NewTimer(dialFallbackDelay)
	defer timer.Stop()
	var failures []error
	for {
		select {
		case result := <-outcomes:
			pending--
			if result.err == nil {
				closeLosers(pending)
				return result.conn, nil
			}
			failures = append(failures, result.err)
			if next < len(ordered) {
				// 前一个已经明确失败，不必再等延迟，立刻试下一个。
				launch()
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(dialFallbackDelay)
			} else if pending == 0 {
				return nil, errors.Join(failures...)
			}
		case <-timer.C:
			if next < len(ordered) {
				launch()
				timer.Reset(dialFallbackDelay)
			}
		case <-ctx.Done():
			closeLosers(pending)
			return nil, errors.Join(append(failures, ctx.Err())...)
		}
	}
}

// interleaveAddressFamilies 保持首选地址在前，之后交替 IPv4 / IPv6，让回退尽快换到另一个地址族。
func interleaveAddressFamilies(addresses []net.IP) []net.IP {
	if len(addresses) < 2 {
		return addresses
	}
	first := addresses[0]
	var same, other []net.IP
	for _, ip := range addresses[1:] {
		if (ip.To4() != nil) == (first.To4() != nil) {
			same = append(same, ip)
		} else {
			other = append(other, ip)
		}
	}
	ordered := []net.IP{first}
	for len(same) > 0 || len(other) > 0 {
		if len(other) > 0 {
			ordered = append(ordered, other[0])
			other = other[1:]
		}
		if len(same) > 0 {
			ordered = append(ordered, same[0])
			same = same[1:]
		}
	}
	return ordered
}
