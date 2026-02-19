package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultMetURL  = "https://api.met.no/weatherapi/locationforecast/2.0/compact"
	defaultGeoURL  = "https://ws.geonorge.no/stedsnavn/v1/sted"
	userAgent      = "yr-no-weather-mcp/1.0 github.com/starefossen/yr-no-weather-mcp"
	defaultPort    = "8080"
	geocodeCacheTTL = 24 * time.Hour
)

// GeoResult represents a geocoding result from Norkart/Geonorge
type GeoResult struct {
	Lat       float64
	Lon       float64
	Name      string
	ExpiresAt time.Time
}

// WeatherCache holds cached weather responses
type WeatherCache struct {
	Data      *MetResponse
	ExpiresAt time.Time
}

// MetResponse represents the MET.no compact forecast response
type MetResponse struct {
	Geometry struct {
		Coordinates []float64 `json:"coordinates"`
	} `json:"geometry"`
	Properties struct {
		Meta struct {
			UpdatedAt string `json:"updated_at"`
		} `json:"meta"`
		Timeseries []Timeseries `json:"timeseries"`
	} `json:"properties"`
}

type Timeseries struct {
	Time string `json:"time"`
	Data struct {
		Instant struct {
			Details struct {
				AirTemperature   float64 `json:"air_temperature"`
				WindSpeed        float64 `json:"wind_speed"`
				WindFromDir      float64 `json:"wind_from_direction"`
				CloudAreaFrac    float64 `json:"cloud_area_fraction"`
				RelativeHumidity float64 `json:"relative_humidity"`
				AirPressure      float64 `json:"air_pressure_at_sea_level"`
			} `json:"details"`
		} `json:"instant"`
		Next1Hours  *ForecastPeriod `json:"next_1_hours,omitempty"`
		Next6Hours  *ForecastPeriod `json:"next_6_hours,omitempty"`
		Next12Hours *ForecastPeriod `json:"next_12_hours,omitempty"`
	} `json:"data"`
}

type ForecastPeriod struct {
	Summary struct {
		SymbolCode string `json:"symbol_code"`
	} `json:"summary"`
	Details struct {
		PrecipitationAmount float64 `json:"precipitation_amount"`
		PrecipitationMin    float64 `json:"precipitation_amount_min"`
		PrecipitationMax    float64 `json:"precipitation_amount_max"`
		AirTemperatureMax   float64 `json:"air_temperature_max"`
		AirTemperatureMin   float64 `json:"air_temperature_min"`
	} `json:"details"`
}

type WeatherServer struct {
	httpClient   *http.Client
	metBaseURL   string
	geoBaseURL   string
	geoCache     map[string]*GeoResult
	geoCacheMu   sync.RWMutex
	weatherCache map[string]*WeatherCache
	weatherMu    sync.RWMutex
}

func NewWeatherServer() *WeatherServer {
	return &WeatherServer{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metBaseURL:   defaultMetURL,
		geoBaseURL:   defaultGeoURL,
		geoCache:     make(map[string]*GeoResult),
		weatherCache: make(map[string]*WeatherCache),
	}
}

// truncateCoord truncates a coordinate to 4 decimal places (MET.no requirement)
func truncateCoord(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// cacheKey returns a cache key for coordinates
func cacheKey(lat, lon float64) string {
	return fmt.Sprintf("%.4f,%.4f", lat, lon)
}

// parseLocation resolves a location string to lat/lon coordinates.
// Supports "lat,lon" format or Norwegian place names via geocoding.
func (ws *WeatherServer) parseLocation(ctx context.Context, location string) (float64, float64, string, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return 0, 0, "", fmt.Errorf("location is required")
	}

	// Try parsing as "lat,lon"
	if parts := strings.SplitN(location, ",", 2); len(parts) == 2 {
		lat, errLat := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lon, errLon := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if errLat == nil && errLon == nil && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 {
			return truncateCoord(lat), truncateCoord(lon), fmt.Sprintf("%.4f, %.4f", lat, lon), nil
		}
	}

	// Geocode the place name
	return ws.geocode(ctx, location)
}

// geocode resolves a Norwegian place name to coordinates using Norkart/Geonorge
func (ws *WeatherServer) geocode(ctx context.Context, name string) (float64, float64, string, error) {
	key := strings.ToLower(name)

	// Check cache
	ws.geoCacheMu.RLock()
	if cached, ok := ws.geoCache[key]; ok && time.Now().Before(cached.ExpiresAt) {
		ws.geoCacheMu.RUnlock()
		return cached.Lat, cached.Lon, cached.Name, nil
	}
	ws.geoCacheMu.RUnlock()

	url := fmt.Sprintf("%s?sok=%s&fuzzy=true&treffPerSide=1&utkoordsys=4258", ws.geoBaseURL, name)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, 0, "", fmt.Errorf("failed to create geocoding request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := ws.httpClient.Do(req)
	if err != nil {
		return 0, 0, "", fmt.Errorf("geocoding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, "", fmt.Errorf("geocoding returned status %d", resp.StatusCode)
	}

	var geoResp struct {
		Navn []struct {
			Stedsnavn []struct {
				Skrivemåte string `json:"skrivemåte"`
			} `json:"stedsnavn"`
			Kommuner []struct {
				Kommunenavn string `json:"kommunenavn"`
			} `json:"kommuner"`
			Representasjonspunkt struct {
				Nord float64 `json:"nord"`
				Øst  float64 `json:"øst"`
			} `json:"representasjonspunkt"`
		} `json:"navn"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, "", fmt.Errorf("failed to read geocoding response: %w", err)
	}

	if err := json.Unmarshal(body, &geoResp); err != nil {
		return 0, 0, "", fmt.Errorf("failed to parse geocoding response: %w", err)
	}

	if len(geoResp.Navn) == 0 {
		return 0, 0, "", fmt.Errorf("location not found: %s", name)
	}

	result := geoResp.Navn[0]
	lat := truncateCoord(result.Representasjonspunkt.Nord)
	lon := truncateCoord(result.Representasjonspunkt.Øst)
	displayName := name
	if len(result.Stedsnavn) > 0 {
		displayName = result.Stedsnavn[0].Skrivemåte
	}
	if len(result.Kommuner) > 0 {
		displayName = fmt.Sprintf("%s, %s", displayName, result.Kommuner[0].Kommunenavn)
	}

	// Cache the result
	ws.geoCacheMu.Lock()
	ws.geoCache[key] = &GeoResult{
		Lat:       lat,
		Lon:       lon,
		Name:      displayName,
		ExpiresAt: time.Now().Add(geocodeCacheTTL),
	}
	ws.geoCacheMu.Unlock()

	return lat, lon, displayName, nil
}

// fetchWeather fetches weather data from MET.no, with caching based on Expires header
func (ws *WeatherServer) fetchWeather(ctx context.Context, lat, lon float64) (*MetResponse, error) {
	key := cacheKey(lat, lon)

	// Check cache
	ws.weatherMu.RLock()
	if cached, ok := ws.weatherCache[key]; ok && time.Now().Before(cached.ExpiresAt) {
		ws.weatherMu.RUnlock()
		return cached.Data, nil
	}
	ws.weatherMu.RUnlock()

	url := fmt.Sprintf("%s?lat=%.4f&lon=%.4f", ws.metBaseURL, lat, lon)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create weather request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := ws.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weather request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MET.no returned status %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress response: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read weather response: %w", err)
	}

	var metResp MetResponse
	if err := json.Unmarshal(body, &metResp); err != nil {
		return nil, fmt.Errorf("failed to parse weather response: %w", err)
	}

	// Determine cache expiry from Expires header
	expiry := time.Now().Add(30 * time.Minute) // default fallback
	if expiresStr := resp.Header.Get("Expires"); expiresStr != "" {
		if t, err := time.Parse(time.RFC1123, expiresStr); err == nil {
			expiry = t
		}
	}

	// Cache the result
	ws.weatherMu.Lock()
	ws.weatherCache[key] = &WeatherCache{
		Data:      &metResp,
		ExpiresAt: expiry,
	}
	ws.weatherMu.Unlock()

	return &metResp, nil
}

// symbolToEmoji maps MET.no weather symbol codes to emoji
func symbolToEmoji(code string) string {
	base := strings.Split(code, "_")[0]
	switch base {
	case "clearsky":
		return "☀️"
	case "fair":
		return "🌤"
	case "partlycloudy":
		return "⛅"
	case "cloudy":
		return "☁️"
	case "rain", "lightrain":
		return "🌧"
	case "heavyrain":
		return "🌧️"
	case "rainshowers", "lightrainshowers":
		return "🌦"
	case "heavyrainshowers":
		return "🌦️"
	case "sleet", "lightsleet", "heavysleet", "sleetshowers":
		return "🌨🌧"
	case "snow", "lightsnow", "heavysnow", "snowshowers":
		return "🌨"
	case "fog":
		return "🌫"
	case "thunder", "rainandthunder", "heavyrainandthunder":
		return "⛈"
	default:
		return "🌡"
	}
}

// windDirection converts degrees to compass direction
func windDirection(deg float64) string {
	dirs := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	idx := int(math.Round(deg/45)) % 8
	return dirs[idx]
}

// getStringArg extracts a string argument from the request
func getStringArg(req mcp.CallToolRequest, key string) (string, bool) {
	v, ok := req.Params.Arguments[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// handleGetForecast handles the get_forecast MCP tool
func (ws *WeatherServer) handleGetForecast(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	location, ok := getStringArg(req, "location")
	if !ok {
		return mcp.NewToolResultError("location parameter is required"), nil
	}

	lat, lon, name, err := ws.parseLocation(ctx, location)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Could not resolve location: %v", err)), nil
	}

	data, err := ws.fetchWeather(ctx, lat, lon)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Could not fetch weather: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Weather forecast for %s (%.4f°N, %.4f°E)\n\n", name, lat, lon))

	// Group timeseries by day, pick representative data
	now := time.Now()
	days := make(map[string][]Timeseries)
	var dayOrder []string

	for _, ts := range data.Properties.Timeseries {
		t, err := time.Parse(time.RFC3339, ts.Time)
		if err != nil {
			continue
		}
		if t.Before(now.Add(-1 * time.Hour)) {
			continue
		}
		dayKey := t.Format("2006-01-02")
		if _, exists := days[dayKey]; !exists {
			dayOrder = append(dayOrder, dayKey)
		}
		days[dayKey] = append(days[dayKey], ts)
	}

	// Limit to 7 days
	if len(dayOrder) > 7 {
		dayOrder = dayOrder[:7]
	}

	for _, dayKey := range dayOrder {
		entries := days[dayKey]
		t, _ := time.Parse("2006-01-02", dayKey)

		dayLabel := t.Format("Mon Jan 2")
		if t.Format("2006-01-02") == now.Format("2006-01-02") {
			dayLabel = "Today"
		} else if t.Format("2006-01-02") == now.Add(24*time.Hour).Format("2006-01-02") {
			dayLabel = "Tomorrow"
		}

		// Find min/max temp and total precip for the day
		minTemp := 100.0
		maxTemp := -100.0
		totalPrecip := 0.0
		var symbol string
		var midWindSpeed float64
		var midWindDir float64

		for _, e := range entries {
			temp := e.Data.Instant.Details.AirTemperature
			if temp < minTemp {
				minTemp = temp
			}
			if temp > maxTemp {
				maxTemp = temp
			}
			if e.Data.Next1Hours != nil {
				totalPrecip += e.Data.Next1Hours.Details.PrecipitationAmount
			}
		}

		// Pick symbol from midday or first entry with a forecast period
		for _, e := range entries {
			t2, _ := time.Parse(time.RFC3339, e.Time)
			if t2.Hour() >= 11 && t2.Hour() <= 14 {
				if e.Data.Next6Hours != nil {
					symbol = e.Data.Next6Hours.Summary.SymbolCode
				} else if e.Data.Next1Hours != nil {
					symbol = e.Data.Next1Hours.Summary.SymbolCode
				}
				midWindSpeed = e.Data.Instant.Details.WindSpeed
				midWindDir = e.Data.Instant.Details.WindFromDir
				break
			}
		}
		if symbol == "" && len(entries) > 0 {
			e := entries[0]
			if e.Data.Next6Hours != nil {
				symbol = e.Data.Next6Hours.Summary.SymbolCode
			} else if e.Data.Next1Hours != nil {
				symbol = e.Data.Next1Hours.Summary.SymbolCode
			}
			midWindSpeed = e.Data.Instant.Details.WindSpeed
			midWindDir = e.Data.Instant.Details.WindFromDir
		}

		emoji := symbolToEmoji(symbol)
		sb.WriteString(fmt.Sprintf("%s %s: %.0f–%.0f°C", emoji, dayLabel, minTemp, maxTemp))
		if totalPrecip > 0.1 {
			sb.WriteString(fmt.Sprintf(", %.1f mm precip", totalPrecip))
		}
		sb.WriteString(fmt.Sprintf(", wind %.1f m/s %s\n", midWindSpeed, windDirection(midWindDir)))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// handleGetHourly handles the get_hourly MCP tool
func (ws *WeatherServer) handleGetHourly(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	location, ok := getStringArg(req, "location")
	if !ok {
		return mcp.NewToolResultError("location parameter is required"), nil
	}

	hours := 12
	if h, ok := getStringArg(req, "hours"); ok {
		if parsed, err := strconv.Atoi(h); err == nil && parsed > 0 && parsed <= 48 {
			hours = parsed
		}
	}

	lat, lon, name, err := ws.parseLocation(ctx, location)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Could not resolve location: %v", err)), nil
	}

	data, err := ws.fetchWeather(ctx, lat, lon)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Could not fetch weather: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Hourly forecast for %s (next %d hours)\n\n", name, hours))

	now := time.Now()
	count := 0

	for _, ts := range data.Properties.Timeseries {
		if count >= hours {
			break
		}

		t, err := time.Parse(time.RFC3339, ts.Time)
		if err != nil || t.Before(now.Add(-30*time.Minute)) {
			continue
		}

		if ts.Data.Next1Hours == nil {
			continue
		}

		emoji := symbolToEmoji(ts.Data.Next1Hours.Summary.SymbolCode)
		d := ts.Data.Instant.Details
		precip := ts.Data.Next1Hours.Details.PrecipitationAmount

		line := fmt.Sprintf("%s %s: %.1f°C", t.Format("15:04"), emoji, d.AirTemperature)
		if precip > 0 {
			line += fmt.Sprintf(", %.1f mm", precip)
		}
		line += fmt.Sprintf(", wind %.1f m/s %s", d.WindSpeed, windDirection(d.WindFromDir))
		line += fmt.Sprintf(", clouds %0.f%%", d.CloudAreaFrac)
		sb.WriteString(line + "\n")
		count++
	}

	if count == 0 {
		sb.WriteString("No hourly data available for the requested period.\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// handleGetPrecipitation handles the get_precipitation MCP tool
func (ws *WeatherServer) handleGetPrecipitation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	location, ok := getStringArg(req, "location")
	if !ok {
		return mcp.NewToolResultError("location parameter is required"), nil
	}

	period := "today"
	if p, ok := getStringArg(req, "period"); ok {
		period = p
	}

	lat, lon, name, err := ws.parseLocation(ctx, location)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Could not resolve location: %v", err)), nil
	}

	data, err := ws.fetchWeather(ctx, lat, lon)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Could not fetch weather: %v", err)), nil
	}

	now := time.Now()
	var startTime, endTime time.Time

	switch period {
	case "today":
		startTime = now
		endTime = time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	case "tomorrow":
		tomorrow := now.Add(24 * time.Hour)
		startTime = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, now.Location())
		endTime = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 23, 59, 59, 0, now.Location())
	case "week":
		startTime = now
		endTime = now.Add(7 * 24 * time.Hour)
	default:
		return mcp.NewToolResultError("period must be 'today', 'tomorrow', or 'week'"), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Precipitation for %s (%s)\n\n", name, period))

	totalPrecip := 0.0
	hasData := false

	for _, ts := range data.Properties.Timeseries {
		t, err := time.Parse(time.RFC3339, ts.Time)
		if err != nil {
			continue
		}
		if t.Before(startTime) || t.After(endTime) {
			continue
		}

		if ts.Data.Next1Hours == nil {
			continue
		}

		precip := ts.Data.Next1Hours.Details.PrecipitationAmount
		symbol := ts.Data.Next1Hours.Summary.SymbolCode
		hasData = true
		totalPrecip += precip

		if precip > 0 {
			precipType := "rain"
			if strings.Contains(symbol, "snow") {
				precipType = "snow"
			} else if strings.Contains(symbol, "sleet") {
				precipType = "sleet"
			}
			emoji := symbolToEmoji(symbol)
			sb.WriteString(fmt.Sprintf("%s %s: %.1f mm %s (%.1f°C)\n",
				t.Format("15:04"), emoji, precip, precipType,
				ts.Data.Instant.Details.AirTemperature))
		}
	}

	if !hasData {
		sb.WriteString("No precipitation data available for this period.\n")
	} else if totalPrecip < 0.1 {
		sb.WriteString("No precipitation expected. ☀️\n")
	}

	sb.WriteString(fmt.Sprintf("\nTotal: %.1f mm\n", totalPrecip))

	return mcp.NewToolResultText(sb.String()), nil
}

func main() {
	ws := NewWeatherServer()

	s := server.NewMCPServer(
		"yr",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	// Tool: get_forecast
	forecastTool := mcp.NewTool("get_forecast",
		mcp.WithDescription("Get a daily weather forecast summary for a Norwegian location (next 7 days). Returns temperature range, precipitation, wind, and conditions per day."),
		mcp.WithString("location",
			mcp.Required(),
			mcp.Description("Norwegian place name (e.g. Bergen, Tromsø, Ålesund) or coordinates as 'lat,lon' (e.g. '60.39,5.32')"),
		),
	)
	s.AddTool(forecastTool, ws.handleGetForecast)

	// Tool: get_hourly
	hourlyTool := mcp.NewTool("get_hourly",
		mcp.WithDescription("Get an hourly weather forecast for a Norwegian location. Returns temperature, precipitation, wind, and cloud cover per hour."),
		mcp.WithString("location",
			mcp.Required(),
			mcp.Description("Norwegian place name or coordinates as 'lat,lon'"),
		),
		mcp.WithString("hours",
			mcp.Description("Number of hours to show (1-48, default 12)"),
		),
	)
	s.AddTool(hourlyTool, ws.handleGetHourly)

	// Tool: get_precipitation
	precipTool := mcp.NewTool("get_precipitation",
		mcp.WithDescription("Get precipitation details for a Norwegian location. Shows when and how much rain/snow is expected."),
		mcp.WithString("location",
			mcp.Required(),
			mcp.Description("Norwegian place name or coordinates as 'lat,lon'"),
		),
		mcp.WithString("period",
			mcp.Description("Time period: 'today', 'tomorrow', or 'week' (default: today)"),
			mcp.Enum("today", "tomorrow", "week"),
		),
	)
	s.AddTool(precipTool, ws.handleGetPrecipitation)

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:" + defaultPort
	}

	sseServer := server.NewSSEServer(s,
		server.WithBaseURL(baseURL),
	)

	// Wrap SSE server with health endpoint
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
			return
		}
		sseServer.ServeHTTP(w, r)
	})

	log.Printf("yr MCP server starting on :%s", defaultPort)
	log.Printf("Health: http://localhost:%s/health", defaultPort)
	log.Printf("MCP SSE: http://localhost:%s/sse", defaultPort)

	httpServer := &http.Server{
		Addr:         ":" + defaultPort,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
