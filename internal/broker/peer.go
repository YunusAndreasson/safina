package broker

import (
	"errors"
	"net"
	"os"
)

func sameUser(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("expected Unix connection")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		uid, e := peerUID(int(fd))
		peerErr = e
		if e == nil && int64(uid) != int64(os.Getuid()) {
			peerErr = errors.New("broker peer belongs to another user")
		}
	})
	return errors.Join(err, peerErr)
}

type ownerListener struct{ net.Listener }

func (l ownerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err = sameUser(conn); err == nil {
			return conn, nil
		}
		_ = conn.Close()
	}
}
