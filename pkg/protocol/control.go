package protocol

type RegisterPayload struct {
	Token   string      `json:"token"`
	Tunnels []TunnelDef `json:"tunnels"`
}

type TunnelDef struct {
	ID          string           `json:"id"`
	Type        string           `json:"type"`
	LocalAddr   string           `json:"local_addr"`
	RequestHost string           `json:"request_host,omitempty"`
	RemotePort  int              `json:"remote_port,omitempty"`
	BasicAuth   *BasicAuthConfig `json:"basic_auth,omitempty"`
}

type BasicAuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RegisterAckPayload struct {
	Success bool              `json:"success"`
	Error   string            `json:"error,omitempty"`
	Tunnels []TunnelAssign    `json:"tunnels,omitempty"`
}

type TunnelAssign struct {
	ID           string `json:"id"`
	PublicURL    string `json:"public_url"`
	SubdomainURL string `json:"subdomain_url,omitempty"`
	Error        string `json:"error,omitempty"`
}

type HTTPRequestPayload struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body,omitempty"`
}

type HTTPResponsePayload struct {
	StatusCode int               `json:"status_code"`
	StatusText string            `json:"status_text"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body,omitempty"`
}
