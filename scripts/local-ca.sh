#!/usr/bin/env bash
# Creates, once, a development CA that can only sign chat.localhost names and
# loopback IPs, then prints its directory. Re-running reuses the existing CA.
set -euo pipefail

dir="${CHAT_CA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/chat-localhost}"
cert="$dir/ca.crt"
key="$dir/ca.key"

if [[ -s "$cert" && -s "$key" ]]; then
  echo "$dir"
  exit 0
fi

mkdir -p "$dir"
chmod 700 "$dir"
umask 077

config="$(mktemp)"
trap 'rm -f "$config"' EXIT
cat >"$config" <<'CONF'
[req]
distinguished_name = dn
x509_extensions = v3_ca
prompt = no

[dn]
CN = chat.localhost development CA

[v3_ca]
basicConstraints = critical, CA:TRUE, pathlen:0
keyUsage = critical, keyCertSign, cRLSign
subjectKeyIdentifier = hash
nameConstraints = critical, permitted;DNS:chat.localhost, permitted;IP:127.0.0.0/255.0.0.0
CONF

openssl ecparam -name prime256v1 -genkey -noout -out "$key"
openssl req -x509 -new -key "$key" -out "$cert" -days 3650 -sha256 -config "$config"
chmod 600 "$key"
chmod 644 "$cert"
echo "$dir"
