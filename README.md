# yr-no-weather-mcp

An [MCP](https://modelcontextprotocol.io/) server providing Norwegian weather data from [MET.no](https://api.met.no) (yr.no) with geocoding via [Norkart/Geonorge](https://ws.geonorge.no). Written in Go.

## Features

- **Three weather tools** — daily forecast, hourly forecast, and precipitation details
- **Norwegian place name geocoding** — just say "Bergen" or "Tromsø"
- **Coordinate support** — pass `lat,lon` directly
- **Caching** — respects MET.no `Expires` headers; geocoding cached for 24h
- **No API keys required** — both MET.no and Geonorge are free public APIs
- **Scale-to-zero ready** — lightweight Go binary, works great on Knative

## Tools

| Tool                | Description                                         |
| ------------------- | --------------------------------------------------- |
| `get_forecast`      | Daily weather summary for next 7 days               |
| `get_hourly`        | Hourly forecast (temperature, wind, precip, clouds) |
| `get_precipitation` | Precipitation details for today/tomorrow/week       |

All tools accept a `location` parameter — either a Norwegian place name (e.g. "Bergen", "Tromsø") or coordinates as `lat,lon` (e.g. "60.39,5.32").

## Quick Start

### Run locally

```bash
go build -o yr .
./yr
```

The server starts on port 8080 by default:
- **Health check**: http://localhost:8080/health
- **MCP SSE endpoint**: http://localhost:8080/sse

### Docker

```bash
# Build
docker build -t yr-no-weather-mcp .

# Run
docker run -p 8080:8080 yr-no-weather-mcp
```

### Environment Variables

| Variable   | Default                    | Description              |
| ---------- | -------------------------- | ------------------------ |
| `BASE_URL` | `http://localhost:8080`    | Public base URL for SSE  |

## Architecture

```
MCP Client
    │ HTTP/SSE (JSON-RPC)
    ▼
yr-no-weather-mcp (:8080)
    │
    ├── api.met.no (weather data)
    └── ws.geonorge.no (geocoding)
```

- Implements the [Model Context Protocol](https://modelcontextprotocol.io/) over HTTP+SSE
- Uses [mcp-go](https://github.com/mark3labs/mcp-go) SDK
- User-Agent header set per [MET.no Terms of Service](https://api.met.no/doc/TermsOfService)
- Stateless — all caching is in-memory

## Testing

```bash
go test -v ./...
```

## Data Sources

- **Weather**: [MET.no Locationforecast 2.0](https://api.met.no/weatherapi/locationforecast/2.0/documentation) — free, no API key required
- **Geocoding**: [Geonorge Stedsnavn API](https://ws.geonorge.no/stedsnavn/v1/) — Norwegian place name search, free

Both APIs are provided by Norwegian government agencies and are free for public use.

## License

MIT
