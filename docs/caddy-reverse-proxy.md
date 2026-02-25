# Securing Ollama with Caddy Reverse Proxy

## Why Use a Reverse Proxy

Ollama has no built-in authentication. When exposing Ollama beyond localhost -- to other machines on your network or the internet -- a reverse proxy provides:

- **TLS encryption** -- automatic via Caddy and Let's Encrypt
- **Authentication** -- Basic Auth or Bearer token
- **Rate limiting and access control** -- restrict who can run inference on your hardware

Without a reverse proxy, anyone who can reach port 11434 has unrestricted access to your models.

## Prerequisites

- Ollama running on the host (default: `localhost:11434`)
- Caddy installed -- see <https://caddyserver.com/docs/install>
- A domain name pointed at your server (required for automatic TLS via Let's Encrypt; for internal networks, see the self-signed option under TLS Configuration)

## Basic Setup (TLS Only, No Auth)

The simplest Caddyfile proxies traffic to Ollama with automatic HTTPS:

```
ollama.example.com {
    reverse_proxy localhost:11434
}
```

Caddy handles certificate provisioning, renewal, and HTTPS redirects automatically. Start Caddy with:

```sh
sudo caddy start --config /etc/caddy/Caddyfile
```

At this point `https://ollama.example.com` serves the Ollama API over TLS, but anyone can access it.

## Adding Basic Auth

Basic Auth adds a username/password challenge to every request.

1. Generate a bcrypt password hash:

   ```sh
   caddy hash-password
   ```

   Enter your chosen password when prompted. Caddy outputs a hash like `$2a$14$...`.

2. Add the `basicauth` directive to your Caddyfile:

   ```
   ollama.example.com {
       basicauth {
           username $2a$14$HASH_HERE
       }
       reverse_proxy localhost:11434
   }
   ```

   Replace `username` with your chosen username and `$2a$14$HASH_HERE` with the hash from step 1.

3. Configure electrictown to authenticate against this endpoint:

   ```yaml
   ollama-network:
     type: ollama
     base_url: https://ollama.example.com
     api_key: "username:password"
     auth_type: basic
   ```

   Use the plaintext username and password here (not the hash). electrictown sends them as a standard `Authorization: Basic ...` header.

## Adding Bearer Token Auth

Bearer token auth is simpler -- a single shared secret instead of a username/password pair.

Caddyfile using header matching:

```
ollama.example.com {
    @unauthorized {
        not header Authorization "Bearer YOUR_SECRET_TOKEN"
    }
    respond @unauthorized 401

    reverse_proxy localhost:11434
}
```

Replace `YOUR_SECRET_TOKEN` with a strong random string (e.g., `openssl rand -hex 32`).

Matching electrictown config:

```yaml
ollama-network:
  type: ollama
  base_url: https://ollama.example.com
  api_key: YOUR_SECRET_TOKEN
```

electrictown sends this as `Authorization: Bearer YOUR_SECRET_TOKEN` by default when `auth_type` is not specified.

## TLS Configuration

**Never expose Ollama over the network without TLS. API keys and model responses transmitted in plaintext can be intercepted.**

### Automatic HTTPS (Public Domains)

If your server is reachable on ports 80 and 443 and your domain's DNS points to it, Caddy provisions a Let's Encrypt certificate automatically. No additional TLS configuration is needed -- the examples above already use automatic HTTPS.

### Self-Signed Certificates (Internal Networks)

For hosts without public DNS (e.g., a homelab or corporate network), use Caddy's internal CA:

```
ollama.internal.lan {
    tls internal
    reverse_proxy localhost:11434
}
```

Caddy generates a self-signed certificate from its own root CA. Clients need to trust Caddy's root certificate (found in Caddy's data directory) or disable certificate verification.

### Reference Links

- Let's Encrypt documentation: <https://letsencrypt.org/docs/>
- Caddy automatic HTTPS: <https://caddyserver.com/docs/automatic-https>
- Caddy TLS directive: <https://caddyserver.com/docs/caddyfile/directives/tls>

## Troubleshooting

### Caddy Fails to Start (Port Conflict)

Another process (often Apache or Nginx) is already bound to port 80 or 443:

```sh
sudo ss -tlnp | grep -E ':80|:443'
```

Stop the conflicting service or configure Caddy to use alternate ports.

### Let's Encrypt Challenges Fail

Automatic certificate provisioning requires:

- Port 80 open inbound (HTTP-01 challenge) or port 443 open inbound (TLS-ALPN-01 challenge)
- DNS A/AAAA record resolving to this server's public IP
- No firewall, CDN, or load balancer intercepting the challenge

Check Caddy logs for specific ACME errors:

```sh
journalctl -u caddy --no-pager | grep -i acme
```

### Ollama Refuses Connections from Caddy

By default, Ollama only listens on `127.0.0.1:11434`. If Caddy runs on the same host, this works. If Caddy runs on a different host, set `OLLAMA_HOST` so Ollama listens on all interfaces:

```sh
OLLAMA_HOST=0.0.0.0 ollama serve
```

Or in a systemd override:

```ini
[Service]
Environment="OLLAMA_HOST=0.0.0.0"
```

### Large Request/Response Bodies

LLM context windows can produce large payloads. Caddy does not impose a request body size limit by default, but if you have added one or use a Caddy module that does, ensure it accommodates your largest expected prompt. Ollama responses stream by default, so response size is rarely an issue.

If you see `413 Request Entity Too Large` or similar errors, check for a `request_body` directive in your Caddyfile and increase or remove the `max_size`:

```
ollama.example.com {
    request_body {
        max_size 100MB
    }
    reverse_proxy localhost:11434
}
```
