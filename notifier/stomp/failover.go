package stomp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	gostomp "github.com/go-stomp/stomp/v3"
	"github.com/quay/clair/config"
	"github.com/quay/zlog"
)

// failOver will return the first successful connection made against the provided
// brokers, or an existing connection if not closed.
//
// failOver is safe for concurrent usage.
type failOver struct {
	tls   *tls.Config
	login *config.Login
	uris  []string
}

// Dial will dial the provided URI in accordance with the provided Config.
//
// Note: the STOMP protocol does not support multiplexing operations over a
// single TCP connection. A TCP connection must be made for each STOMP
// connection.
func (f *failOver) Dial(ctx context.Context, uri string) (*gostomp.Conn, error) {
	var opts []func(*gostomp.Conn) error
	if f.login != nil {
		opts = append(opts, gostomp.ConnOpt.Login(f.login.Login, f.login.Passcode))
	}

	var d interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	} = &net.Dialer{
		Timeout: 2 * time.Second,
	}
	if f.tls != nil {
		d = &tls.Dialer{
			NetDialer: d.(*net.Dialer),
			Config:    f.tls,
		}
	}
	conn, err := d.DialContext(ctx, "tcp", uri)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to broker @ %v: %w", uri, err)
	}

	stompConn, err := gostomp.Connect(conn, opts...)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		return nil, fmt.Errorf("stomp connect handshake to broker @ %v failed: %w", uri, err)
	}

	return stompConn, err
}

// Connection returns a new connection to the first broker that successfully
// handshakes.
//
// The caller MUST call conn.Disconnect() to close the underlying TCP connection
// when finished.
func (f *failOver) Connection(ctx context.Context) (*gostomp.Conn, error) {
	ctx = zlog.ContextWithValues(ctx, "component", "notifier/stomp/failOver.Connection")

	for _, uri := range f.uris {
		conn, err := f.Dial(ctx, uri)
		if err != nil {
			zlog.Debug(ctx).
				Str("broker", uri).
				Err(err).
				Msg("failed to dial broker. attempting next")
			continue
		}
		return conn, nil
	}
	return nil, fmt.Errorf("exhausted all brokers and unable to make connection")
}
