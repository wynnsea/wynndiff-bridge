# WynnDiff Bridge (Go)

Server ringan (zero external dependencies) untuk menghubungkan aplikasi **WynnDiff** di HP ke PC Windows/Linux lokal lewat **Tailscale** — memungkinkan remote coding dengan OpenRouter, melihat **Git Diff** asli, serta **Commit & Push** langsung ke GitHub.

## Cara Jalankan di PC Windows

1. **Install Tailscale** di PC Windows dan di HP (login akun yang sama). Catat IP Tailscale PC Windows (misal `100.x.y.z`).
2. Copy `.env.example` menjadi `.env`, lalu isi:
   ```env
   PORT=8787
   BIND_ADDR=0.0.0.0
   REPO_DIR=C:\Users\NamaKamu\Documents\project-percobaan
   BRIDGE_TOKEN=token-rahasia-kamu
   OPENROUTER_API_KEY=sk-or-v1-key-kamu
   AI_GATEWAY_URL=https://openrouter.ai/api/v1
   ```
3. Jalankan bridge:
   - Jika pakai binary `.exe` (tanpa perlu install Go di Windows):
     ```powershell
     .\wynndiff-bridge.exe
     ```
   - Atau jika Go sudah terpasang di Windows:
     ```powershell
     go run ./cmd/bridge
     ```
4. Di aplikasi **WynnDiff** di HP → buka tab **Host**:
   - Isi **Alamat Host** = IP Tailscale PC Windows (`100.x.y.z`)
   - Isi **Port** = `8787`
   - Isi **Bridge Token** = sama dengan `BRIDGE_TOKEN` di `.env` PC
   - Aktifkan **Mode Remote Bridge** → ketuk **Tes koneksi**!

## Build Binary Windows dari Linux

```bash
GOOS=windows GOARCH=amd64 go build -o dist/wynndiff-bridge.exe ./cmd/bridge
```
