package gateway

import (
	"net"
	"sync"

	"m-tunnel/internal/protocol"
)

type publicStream struct {
	session *agentSession
	public  net.Conn
	id      uint32

	closeOnce sync.Once
}

func (s *publicStream) copyPublicToAgent() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.public.Read(buf)
		if n > 0 {
			s.session.bytesIn.Add(int64(n))
			frame, encErr := protocol.Encode(protocol.TypeData, s.id, buf[:n])
			if encErr != nil {
				s.session.closeStream(s.id, "encode failed")
				return
			}
			if writeErr := s.session.writeRaw(frame); writeErr != nil {
				s.session.removeStream(s.id, "agent write failed")
				return
			}
		}
		if err != nil {
			s.session.closeStream(s.id, "public closed")
			return
		}
	}
}

func (s *publicStream) writeFromAgent(data []byte) error {
	_, err := s.public.Write(data)
	return err
}

func (s *publicStream) close(reason string) {
	s.closeOnce.Do(func() {
		_ = s.public.Close()
		s.session.gw.log.Info("stream closed", "client_id", s.session.cfg.ID,
			"stream_id", s.id, "reason", reason,
			"bytes_in", s.session.bytesIn.Load(), "bytes_out", s.session.bytesOut.Load())
	})
}
