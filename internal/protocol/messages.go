package protocol

const (
	TypeRegister    uint8 = 1
	TypeRegisterOK  uint8 = 2
	TypeOpenStream  uint8 = 3
	TypeData        uint8 = 4
	TypeCloseStream uint8 = 5
	TypePing        uint8 = 6
	TypePong        uint8 = 7
	TypeError       uint8 = 8
)

const HeaderSize = 9

type RegisterPayload struct {
	ClientID string `json:"clientId"`
	Token    string `json:"token"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
}

type OpenStreamPayload struct {
	RemoteAddr string `json:"remoteAddr"`
}
