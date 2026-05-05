#!/usr/bin/env bash
set -euo pipefail

# ╔══════════════════════════════════════════════════════════════╗
# ║  Canal 自动 HTTPS 证书脚本 (acme.sh + 泛域名)               ║
# ║  使用前准备：DNS 服务商的 API 密钥                          ║
# ╚══════════════════════════════════════════════════════════════╝

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${CYAN}[INFO]${NC} $1"; }
ok()    { echo -e "${GREEN}[OK]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
err()   { echo -e "${RED}[ERR]${NC} $1"; }

# ── 检查 root ──────────────────────────────────────────────────
if [ "$EUID" -ne 0 ]; then
  warn "建议以 root 用户运行，避免证书文件权限问题"
fi

# ── 安装 acme.sh ────────────────────────────────────────────────
install_acme() {
  if command -v acme.sh &>/dev/null; then
    ok "acme.sh 已安装: $(acme.sh --version 2>&1 | head -1)"
    return
  fi
  info "正在安装 acme.sh ..."
  curl -fsSL https://get.acme.sh | sh
  source ~/.bashrc 2>/dev/null || true
  if command -v acme.sh &>/dev/null; then
    ok "acme.sh 安装成功"
  else
    err "acme.sh 安装失败，请手动安装: curl https://get.acme.sh | sh"
    exit 1
  fi
}

# ── 选择 DNS 提供商 ────────────────────────────────────────────
select_provider() {
  echo ""
  echo "选择 DNS 服务商:"
  echo "  1) 阿里云 (Aliyun DNS)"
  echo "  2) 腾讯云 (DNSPod)"
  echo "  3) Cloudflare"
  echo "  4) AWS Route53"
  echo "  5) 其他（手动输入 acme.sh DNS 参数）"
  echo ""
  read -rp "请输入编号 [1-5]: " provider
  case "$provider" in
    1) DNS_PROVIDER="dns_ali"
       info "请设置阿里云 DNS 密钥（https://ram.console.aliyun.com/manage/ak）"
       [[ -z "${Ali_Key:-}" ]] && read -rp "Ali_Key (AccessKeyId): " Ali_Key && export Ali_Key
       [[ -z "${Ali_Secret:-}" ]] && read -rp "Ali_Secret (AccessKeySecret): " Ali_Secret && export Ali_Secret
       ;;
    2) DNS_PROVIDER="dns_dp"
       info "请设置 DNSPod 密钥（https://console.dnspod.cn/account/token）"
       [[ -z "${DP_Id:-}" ]] && read -rp "DP_Id: " DP_Id && export DP_Id
       [[ -z "${DP_Key:-}" ]] && read -rp "DP_Key: " DP_Key && export DP_Key
       ;;
    3) DNS_PROVIDER="dns_cf"
       info "请设置 Cloudflare API Token（https://dash.cloudflare.com/profile/api-tokens）"
       [[ -z "${CF_Token:-}" ]] && read -rp "CF_Token: " CF_Token && export CF_Token
       ;;
    4) DNS_PROVIDER="dns_aws"
       info "请设置 AWS 凭证（需要 Route53 权限）"
       [[ -z "${AWS_ACCESS_KEY_ID:-}" ]] && read -rp "AWS_ACCESS_KEY_ID: " AWS_ACCESS_KEY_ID && export AWS_ACCESS_KEY_ID
       [[ -z "${AWS_SECRET_ACCESS_KEY:-}" ]] && read -rp "AWS_SECRET_ACCESS_KEY: " AWS_SECRET_ACCESS_KEY && export AWS_SECRET_ACCESS_KEY
       ;;
    5) echo ""
       read -rp "请输入 acme.sh --dns 参数值 (如 dns_cf, dns_ali): " DNS_PROVIDER
       info "请自行设置该 DNS 提供商所需的环境变量（参考 acme.sh 文档）"
       ;;
    *) err "无效选择"; exit 1 ;;
  esac
}

# ── 签发证书 ────────────────────────────────────────────────────
issue_cert() {
  echo ""
  read -rp "请输入根域名 (如 example.com): " ROOT_DOMAIN
  if [ -z "$ROOT_DOMAIN" ]; then err "域名不能为空"; exit 1; fi

  # 检查是否已有证书
  local cert_dir="$HOME/.acme.sh/${ROOT_DOMAIN}_ecc"
  if [ -f "${cert_dir}/fullchain.cer" ]; then
    warn "检测到已有证书: ${cert_dir}"
    read -rp "是否重新签发？(y/N): " renew_ans
    if [[ "$renew_ans" =~ ^[Yy]$ ]]; then
      info "正在重新签发证书 ..."
      acme.sh --renew -d "$ROOT_DOMAIN" -d "*.$ROOT_DOMAIN" --force || true
    else
      ok "使用现有证书"
    fi
  else
    info "正在为 ${ROOT_DOMAIN} 和 *.${ROOT_DOMAIN} 签发证书 ..."
    info "DNS 验证中，可能需要等待 30-120 秒 ..."
    if ! acme.sh --issue --dns "$DNS_PROVIDER" -d "$ROOT_DOMAIN" -d "*.$ROOT_DOMAIN"; then
      err "证书签发失败"
      info "常见原因:"
      info "  - DNS API 密钥不正确"
      info "  - 域名未在 DNS 服务商处管理"
      info "  - DNS 未正确配置泛解析 A 记录"
      exit 1
    fi
    ok "证书签发成功！"
  fi
}

# ── 部署到固定目录 ──────────────────────────────────────────────
deploy_cert() {
  echo ""
  info "部署证书到固定目录: /etc/canal/certs/"
  mkdir -p /etc/canal/certs

  local cert_src="$HOME/.acme.sh/${ROOT_DOMAIN}_ecc"
  if [ ! -f "${cert_src}/fullchain.cer" ]; then
    cert_src="$HOME/.acme.sh/${ROOT_DOMAIN}"
  fi

  local fullchain="/etc/canal/certs/fullchain.pem"
  local privkey="/etc/canal/certs/privkey.pem"

  acme.sh --install-cert -d "$ROOT_DOMAIN" \
    --fullchain-file "$fullchain" \
    --key-file "$privkey" \
    --reloadcmd "systemctl restart canal-server 2>/dev/null || killall -HUP canal-server 2>/dev/null || true"

  chmod 644 "$fullchain"
  chmod 600 "$privkey"
  ok "证书已部署:"
  ok "  证书: $fullchain"
  ok "  私钥: $privkey"
}

# ── 输出启动命令 ────────────────────────────────────────────────
print_summary() {
  echo ""
  echo "╔══════════════════════════════════════════════════════════╗"
  echo "║                 配置完成                                ║"
  echo "╚══════════════════════════════════════════════════════════╝"
  echo ""
  echo "Canal 服务启动命令:"
  echo ""
  echo "canal-server \\"
  echo "  --host ${ROOT_DOMAIN} \\"
  echo "  --addr :7000 \\"
  echo "  --tls-cert /etc/canal/certs/fullchain.pem \\"
  echo "  --tls-key /etc/canal/certs/privkey.pem \\"
  echo "  --proxy-addr :443 \\"
  echo "  --dashboard-addr :8443 \\"
  echo "  --token-file /etc/canal/tokens.yaml \\"
  echo "  --user-file /etc/canal/users.yaml" \
       "${ADMIN_ARGS:-}"
  echo ""
  echo "客户端连接地址:"
  echo "  wss://${ROOT_DOMAIN}:7000"
  echo ""
  echo "Dashboard 访问地址:"
  echo "  https://${ROOT_DOMAIN}:8443"
  echo ""
  echo "子域名隧道地址:"
  echo "  https://tun-xxx.${ROOT_DOMAIN}:443"
  echo ""

  # 写入配置文件
  cat > /etc/canal/server.yaml <<EOF
# canal server config (auto-generated by setup-ssl.sh)
listen_addr: ":7000"
public_host: "${ROOT_DOMAIN}"
tls_cert_file: "/etc/canal/certs/fullchain.pem"
tls_key_file: "/etc/canal/certs/privkey.pem"
token_file: "/etc/canal/tokens.yaml"
user_file: "/etc/canal/users.yaml"
dashboard_addr: ":8443"
proxy_addr: ":443"
http_port_range: "18080-18180"
tcp_port_range: "19000-19100"
EOF
  ok "配置文件已生成: /etc/canal/server.yaml"
  echo ""
  info "证书续期由 acme.sh 自动管理（每天检查）"
  info "续期后自动重启 canal-server 加载新证书"
  info ""
  info "如果 canal 不是通过 systemd 管理，请修改 --reloadcmd:"
  info "  acme.sh --install-cert -d ${ROOT_DOMAIN} --reloadcmd \"your-reload-command\""
}

# ── 主流程 ──────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════╗"
echo "║       Canal HTTPS 证书自动配置脚本                      ║"
echo "║       基于 acme.sh + DNS-API 泛域名证书                 ║"
echo "╚══════════════════════════════════════════════════════════╝"

install_acme
select_provider
issue_cert
deploy_cert
print_summary

ok "全部完成！请启动 canal-server 即可使用 HTTPS/WSS"
