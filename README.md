# WA Copilot API (Back)

## Configuracao por `.env`

1. Crie o arquivo `.env` na pasta `extensao-whatsapp-back`:

```env
PORT=8080
API_AUTH_TOKEN=dev-token
OPENROUTER_API_KEY=your-openrouter-key
OPENROUTER_BASE_URL=https://openrouter.ai/api/v1
```

2. Execute sem passar variaveis no comando:

```bash
go run ./cmd/api
```

O carregamento de `.env` e `.env.local` acontece automaticamente no bootstrap da API.

