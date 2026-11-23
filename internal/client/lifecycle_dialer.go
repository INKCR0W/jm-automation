package client

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/INKCR0W/jm-automation/pkg/logger"
)

func newLifecycleDialer(lifetime context.Context, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := lifetime.Err(); err != nil {
			return nil, err
		}

		// HTTP/2 重拨使用传输层自己的 context，仍需在退出时中断 TCP 拨号
		dialCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stopDial := context.AfterFunc(lifetime, cancel)
		defer stopDial()

		conn, err := dialer.DialContext(dialCtx, network, addr)
		if err != nil {
			if lifetimeErr := lifetime.Err(); lifetimeErr != nil {
				return nil, lifetimeErr
			}
			return nil, err
		}

		// TCP 拨号完成后，TLS 握手仍可能卡住。直接关闭原始套接字可以唤醒等待方
		stopClose := context.AfterFunc(lifetime, func() {
			if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				logger.Debug("关闭客户端连接失败", "error", err)
			}
		})
		return &lifecycleConn{Conn: conn, stopClose: stopClose}, nil
	}
}

type lifecycleConn struct {
	net.Conn
	stopClose func() bool
}

func (c *lifecycleConn) Close() error {
	c.stopClose()
	return c.Conn.Close()
}
