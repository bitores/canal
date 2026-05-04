package tunnel

type TunnelState string

const (
	TunnelActive  TunnelState = "active"
	TunnelClosed  TunnelState = "closed"
	TunnelError   TunnelState = "error"
)

type TunnelInfo struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	LocalAddr string      `json:"local_addr"`
	PublicURL string      `json:"public_url"`
	State     TunnelState `json:"state"`
}
