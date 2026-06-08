// Package protocol định nghĩa khung (frame) nhị phân trao đổi giữa Gateway và Agent
// qua WebSocket binary message.
//
// Mỗi frame là một WebSocket binary message độc lập với layout big-endian:
//
//	+--------+-------------+-----------+------------------+
//	| type   | stream_id   | length    | payload          |
//	| uint8  | uint32      | uint32    | length byte      |
//	+--------+-------------+-----------+------------------+
//
// Header cố định 9 byte. DATA payload là raw TCP bytes; REGISTER/OPEN_STREAM
// payload là JSON; CLOSE_STREAM/ERROR payload là chuỗi reason tùy chọn.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MaxPayloadSize giới hạn kích thước payload để tránh cấp phát bộ nhớ bất thường
// khi nhận frame lỗi/độc hại. 16 MiB dư cho mọi gói TCP của SQL Server.
const MaxPayloadSize = 16 << 20

var ErrPayloadTooLarge = errors.New("protocol: payload exceeds maximum size")

// Frame là một thông điệp đã giải mã.
type Frame struct {
	Type     uint8
	StreamID uint32
	Payload  []byte
}

// Encode serialize frame thành một slice byte sẵn sàng gửi như WebSocket binary message.
func Encode(typ uint8, streamID uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}
	buf := make([]byte, HeaderSize+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], streamID)
	binary.BigEndian.PutUint32(buf[5:9], uint32(len(payload)))
	copy(buf[HeaderSize:], payload)
	return buf, nil
}

// Decode parse một WebSocket binary message thành Frame.
func Decode(msg []byte) (Frame, error) {
	if len(msg) < HeaderSize {
		return Frame{}, fmt.Errorf("protocol: frame too short: %d bytes", len(msg))
	}
	length := binary.BigEndian.Uint32(msg[5:9])
	if length > MaxPayloadSize {
		return Frame{}, ErrPayloadTooLarge
	}
	if int(length) != len(msg)-HeaderSize {
		return Frame{}, fmt.Errorf("protocol: length mismatch: header=%d actual=%d", length, len(msg)-HeaderSize)
	}
	f := Frame{
		Type:     msg[0],
		StreamID: binary.BigEndian.Uint32(msg[1:5]),
	}
	if length > 0 {
		// Copy để không giữ tham chiếu tới buffer của thư viện WebSocket.
		f.Payload = make([]byte, length)
		copy(f.Payload, msg[HeaderSize:])
	}
	return f, nil
}
