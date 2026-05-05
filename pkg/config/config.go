package config

type ServerConfig struct {
	ListenAddr    string `yaml:"listen_addr"`
	PublicHost    string `yaml:"public_host"`
	TLSCertFile   string `yaml:"tls_cert_file"`
	TLSKeyFile    string `yaml:"tls_key_file"`
	TokenFile     string `yaml:"token_file"`
	DashboardAddr string `yaml:"dashboard_addr"`
	HTTPPortRange string `yaml:"http_port_range"`
	TCPPortRange  string `yaml:"tcp_port_range"`
	ProxyAddr     string `yaml:"proxy_addr"`
	UserFile      string `yaml:"user_file"`
	AdminUser     string `yaml:"admin_user"`
	AdminPass     string `yaml:"admin_pass"`
}

type ClientConfig struct {
	ServerAddr string      `yaml:"server_addr"`
	AuthToken  string      `yaml:"auth_token"`
	Tunnels    []TunnelCfg `yaml:"tunnels"`
}

type TunnelCfg struct {
	ID          string `yaml:"id"`
	Type        string `yaml:"type"`
	LocalAddr   string `yaml:"local_addr"`
	RequestHost string `yaml:"request_host,omitempty"`
	RemotePort  int    `yaml:"remote_port,omitempty"`
}

func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		ListenAddr:    ":7000",
		PublicHost:    "localhost",
		DashboardAddr: ":8080",
		HTTPPortRange: "18080-18180",
		TCPPortRange:  "19000-19100",
		ProxyAddr:     ":8081",
	}
}

func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ServerAddr: "ws://localhost:7000",
	}
}
