package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestTruncateCoord(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{60.391234567, 60.3912},
		{5.32, 5.32},
		{-15.99999, -16.0},
		{0.0, 0.0},
	}
	for _, tt := range tests {
		got := truncateCoord(tt.input)
		if got != tt.want {
			t.Errorf("truncateCoord(%f) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestCacheKey(t *testing.T) {
	key := cacheKey(60.3912, 5.3200)
	if key != "60.3912,5.3200" {
		t.Errorf("cacheKey = %q, want %q", key, "60.3912,5.3200")
	}
}

func TestWindDirection(t *testing.T) {
	tests := []struct {
		deg  float64
		want string
	}{
		{0, "N"},
		{45, "NE"},
		{90, "E"},
		{135, "SE"},
		{180, "S"},
		{225, "SW"},
		{270, "W"},
		{315, "NW"},
		{360, "N"},
		{22, "N"},
		{23, "NE"},
	}
	for _, tt := range tests {
		got := windDirection(tt.deg)
		if got != tt.want {
			t.Errorf("windDirection(%f) = %q, want %q", tt.deg, got, tt.want)
		}
	}
}

func TestSymbolToEmoji(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"clearsky_day", "☀️"},
		{"clearsky_night", "☀️"},
		{"rain", "🌧"},
		{"snow_day", "🌨"},
		{"fog", "🌫"},
		{"thunder", "⛈"},
		{"unknown_symbol", "🌡"},
	}
	for _, tt := range tests {
		got := symbolToEmoji(tt.code)
		if got != tt.want {
			t.Errorf("symbolToEmoji(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestGetStringArg(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"location": "Bergen",
		"empty":    "",
		"number":   42,
	}

	if v, ok := getStringArg(req, "location"); !ok || v != "Bergen" {
		t.Errorf("getStringArg(location) = %q, %v; want Bergen, true", v, ok)
	}
	if _, ok := getStringArg(req, "missing"); ok {
		t.Error("getStringArg(missing) should return false")
	}
	if _, ok := getStringArg(req, "empty"); ok {
		t.Error("getStringArg(empty) should return false for empty string")
	}
	if _, ok := getStringArg(req, "number"); ok {
		t.Error("getStringArg(number) should return false for non-string")
	}
}

func TestParseLocationCoordinates(t *testing.T) {
	ws := NewWeatherServer()
	ctx := context.Background()

	lat, lon, name, err := ws.parseLocation(ctx, "60.39, 5.32")
	if err != nil {
		t.Fatalf("parseLocation returned error: %v", err)
	}
	if lat != 60.39 || lon != 5.32 {
		t.Errorf("parseLocation = (%f, %f), want (60.39, 5.32)", lat, lon)
	}
	if name == "" {
		t.Error("parseLocation should return a display name")
	}
}

func TestParseLocationEmpty(t *testing.T) {
	ws := NewWeatherServer()
	ctx := context.Background()

	_, _, _, err := ws.parseLocation(ctx, "")
	if err == nil {
		t.Error("parseLocation('') should return error")
	}

	_, _, _, err = ws.parseLocation(ctx, "   ")
	if err == nil {
		t.Error("parseLocation('   ') should return error")
	}
}

func TestParseLocationInvalidCoords(t *testing.T) {
	ws := NewWeatherServer()
	ws.httpClient = &http.Client{Timeout: 1 * time.Millisecond}
	ctx := context.Background()

	// Out of range lat — should fall through to geocoding (which will fail with our short timeout)
	_, _, _, err := ws.parseLocation(ctx, "91.0, 5.0")
	if err == nil {
		t.Error("parseLocation with lat=91 should fail")
	}
}

func TestGeocodeCache(t *testing.T) {
	ws := NewWeatherServer()

	// Seed the cache
	ws.geoCache["bergen"] = &GeoResult{
		Lat:       60.3913,
		Lon:       5.3221,
		Name:      "Bergen, Bergen",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	lat, lon, name, err := ws.geocode(context.Background(), "Bergen")
	if err != nil {
		t.Fatalf("geocode returned error: %v", err)
	}
	if lat != 60.3913 || lon != 5.3221 {
		t.Errorf("geocode cached = (%f, %f), want (60.3913, 5.3221)", lat, lon)
	}
	if name != "Bergen, Bergen" {
		t.Errorf("geocode cached name = %q, want %q", name, "Bergen, Bergen")
	}
}

func TestGeocodeCacheExpired(t *testing.T) {
	ws := NewWeatherServer()
	ws.httpClient = &http.Client{Timeout: 1 * time.Millisecond}

	// Seed with expired entry
	ws.geoCache["expired"] = &GeoResult{
		Lat:       60.0,
		Lon:       5.0,
		Name:      "Expired",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	// Should try to fetch (and fail with short timeout)
	_, _, _, err := ws.geocode(context.Background(), "expired")
	if err == nil {
		t.Error("geocode with expired cache and short timeout should fail")
	}
}

func TestGeocodeURLEncoding(t *testing.T) {
	var receivedRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"navn": []map[string]interface{}{
				{
					"stedsnavn":            []map[string]interface{}{{"skrivemåte": "Haugastøl"}},
					"kommuner":             []map[string]interface{}{{"kommunenavn": "Hol"}},
					"representasjonspunkt": map[string]interface{}{"nord": 60.512, "øst": 7.8631},
				},
			},
		})
	}))
	defer srv.Close()

	ws := NewWeatherServer()
	ws.geoBaseURL = srv.URL

	_, _, _, err := ws.geocode(context.Background(), "Haugastøl")
	if err != nil {
		t.Fatalf("geocode failed: %v", err)
	}

	// The ø must be percent-encoded in the query string
	if !strings.Contains(receivedRawQuery, "sok=Haugast") {
		t.Fatalf("unexpected query: %s", receivedRawQuery)
	}
	if strings.Contains(receivedRawQuery, "sok=Haugastøl") {
		t.Errorf("query contains raw UTF-8 ø — must be percent-encoded: %s", receivedRawQuery)
	}
}

func TestWeatherCacheHit(t *testing.T) {
	ws := NewWeatherServer()

	cached := &MetResponse{}
	cached.Properties.Timeseries = []Timeseries{
		{Time: time.Now().Format(time.RFC3339)},
	}

	ws.weatherCache["60.3900,5.3200"] = &WeatherCache{
		Data:      cached,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	result, err := ws.fetchWeather(context.Background(), 60.39, 5.32)
	if err != nil {
		t.Fatalf("fetchWeather returned error: %v", err)
	}
	if len(result.Properties.Timeseries) != 1 {
		t.Errorf("expected 1 timeseries from cache, got %d", len(result.Properties.Timeseries))
	}
}

// Mock Geonorge server
func newMockGeoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sok := r.URL.Query().Get("sok")
		locations := map[string]struct {
			coords  [2]float64
			kommune string
		}{
			"Bergen":    {coords: [2]float64{60.3932, 5.3245}, kommune: "Bergen"},
			"bergen":    {coords: [2]float64{60.3932, 5.3245}, kommune: "Bergen"},
			"Oslo":      {coords: [2]float64{59.9133, 10.7389}, kommune: "Oslo"},
			"oslo":      {coords: [2]float64{59.9133, 10.7389}, kommune: "Oslo"},
			"Tromsø":    {coords: [2]float64{69.6496, 18.9560}, kommune: "Tromsø"},
			"tromsø":    {coords: [2]float64{69.6496, 18.9560}, kommune: "Tromsø"},
			"Haugastøl": {coords: [2]float64{60.5120, 7.8631}, kommune: "Hol"},
			"haugastøl": {coords: [2]float64{60.5120, 7.8631}, kommune: "Hol"},
			"Ålesund":   {coords: [2]float64{62.4722, 6.1549}, kommune: "Ålesund"},
			"ålesund":   {coords: [2]float64{62.4722, 6.1549}, kommune: "Ålesund"},
			"Ærøy":      {coords: [2]float64{59.0167, 5.7833}, kommune: "Stavanger"},
			"ærøy":      {coords: [2]float64{59.0167, 5.7833}, kommune: "Stavanger"},
		}
		loc, ok := locations[sok]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"navn": []interface{}{}})
			return
		}
		resp := map[string]interface{}{
			"navn": []map[string]interface{}{
				{
					"stedsnavn": []map[string]interface{}{
						{"skrivemåte": sok},
					},
					"kommuner": []map[string]interface{}{
						{"kommunenavn": loc.kommune},
					},
					"representasjonspunkt": map[string]interface{}{
						"nord": loc.coords[0],
						"øst":  loc.coords[1],
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// Mock MET.no server for handler tests
func newMockMetServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now().UTC()
	resp := MetResponse{}
	for i := 0; i < 24; i++ {
		ts := Timeseries{
			Time: now.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		}
		ts.Data.Instant.Details.AirTemperature = 8.0 + float64(i%5)
		ts.Data.Instant.Details.WindSpeed = 3.5
		ts.Data.Instant.Details.WindFromDir = 225
		ts.Data.Instant.Details.CloudAreaFrac = 50
		ts.Data.Next1Hours = &ForecastPeriod{}
		ts.Data.Next1Hours.Summary.SymbolCode = "rain"
		ts.Data.Next1Hours.Details.PrecipitationAmount = 0.5
		resp.Properties.Timeseries = append(resp.Properties.Timeseries, ts)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Expires", now.Add(30*time.Minute).Format(time.RFC1123))
		json.NewEncoder(w).Encode(resp)
	}))
}

func newTestWeatherServer(t *testing.T, metURL, geoURL string) *WeatherServer {
	t.Helper()
	ws := NewWeatherServer()
	if metURL != "" {
		ws.metBaseURL = metURL
	}
	if geoURL != "" {
		ws.geoBaseURL = geoURL
	}
	return ws
}

func callTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("tool handler returned error: %v", err)
	}
	return result
}

func getResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("result content is not TextContent: %T", result.Content[0])
	}
	return tc.Text
}

func TestHandleGetForecastMissingLocation(t *testing.T) {
	ws := NewWeatherServer()
	result := callTool(t, ws.handleGetForecast, map[string]interface{}{})
	if !result.IsError {
		t.Error("expected error result for missing location")
	}
}

func TestHandleGetForecastWithMock(t *testing.T) {
	mock := newMockMetServer(t)
	defer mock.Close()
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := newTestWeatherServer(t, mock.URL, geo.URL)

	result := callTool(t, ws.handleGetForecast, map[string]interface{}{
		"location": "Bergen",
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := getResultText(t, result)
	if text == "" {
		t.Error("expected non-empty forecast text")
	}
	if !contains(text, "Bergen") {
		t.Errorf("forecast should mention Bergen, got: %s", text)
	}
}

func TestHandleGetForecastNorwegianLocations(t *testing.T) {
	mock := newMockMetServer(t)
	defer mock.Close()
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := newTestWeatherServer(t, mock.URL, geo.URL)

	locations := []struct {
		location string
		wantIn   string
	}{
		{"Haugastøl", "Haugastøl"},
		{"Tromsø", "Tromsø"},
		{"Ålesund", "Ålesund"},
		{"Ærøy", "Ærøy"},
	}

	for _, tt := range locations {
		t.Run(tt.location, func(t *testing.T) {
			result := callTool(t, ws.handleGetForecast, map[string]interface{}{
				"location": tt.location,
			})
			if result.IsError {
				t.Fatalf("get_forecast(%q) failed: %v", tt.location, result.Content)
			}
			text := getResultText(t, result)
			if !contains(text, tt.wantIn) {
				t.Errorf("forecast should mention %q, got: %s", tt.wantIn, text)
			}
		})
	}
}

func TestHandleGetHourlyMissingLocation(t *testing.T) {
	ws := NewWeatherServer()
	result := callTool(t, ws.handleGetHourly, map[string]interface{}{})
	if !result.IsError {
		t.Error("expected error result for missing location")
	}
}

func TestHandleGetHourlyWithMock(t *testing.T) {
	mock := newMockMetServer(t)
	defer mock.Close()
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := newTestWeatherServer(t, mock.URL, geo.URL)

	result := callTool(t, ws.handleGetHourly, map[string]interface{}{
		"location": "Bergen",
		"hours":    "6",
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := getResultText(t, result)
	if !contains(text, "Bergen") {
		t.Errorf("hourly should mention Bergen, got: %s", text)
	}
	if !contains(text, "next 6 hours") {
		t.Errorf("hourly should mention 6 hours, got: %s", text)
	}
}

func TestHandleGetPrecipitationMissingLocation(t *testing.T) {
	ws := NewWeatherServer()
	result := callTool(t, ws.handleGetPrecipitation, map[string]interface{}{})
	if !result.IsError {
		t.Error("expected error result for missing location")
	}
}

func TestHandleGetPrecipitationWithMock(t *testing.T) {
	mock := newMockMetServer(t)
	defer mock.Close()
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := newTestWeatherServer(t, mock.URL, geo.URL)

	result := callTool(t, ws.handleGetPrecipitation, map[string]interface{}{
		"location": "Bergen",
		"period":   "today",
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := getResultText(t, result)
	if !contains(text, "Bergen") {
		t.Errorf("precipitation should mention Bergen, got: %s", text)
	}
	if !contains(text, "Total:") {
		t.Errorf("precipitation should contain Total, got: %s", text)
	}
}

func TestHandleGetPrecipitationInvalidPeriod(t *testing.T) {
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := newTestWeatherServer(t, "", geo.URL)
	ws.weatherCache[cacheKey(60.3932, 5.3245)] = &WeatherCache{
		Data:      &MetResponse{},
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	result := callTool(t, ws.handleGetPrecipitation, map[string]interface{}{
		"location": "Bergen",
		"period":   "invalid",
	})
	if !result.IsError {
		t.Error("expected error for invalid period")
	}
}

func TestHealthEndpoint(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "ok")
			return
		}
		http.NotFound(w, r)
	})

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("health returned %d, want 200", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("health body = %q, want %q", rr.Body.String(), "ok")
	}
}

func TestGeocodeWithMock(t *testing.T) {
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := NewWeatherServer()
	ws.geoBaseURL = geo.URL

	lat, lon, name, err := ws.geocode(context.Background(), "Tromsø")
	if err != nil {
		t.Fatalf("geocode returned error: %v", err)
	}
	if lat != 69.6496 {
		t.Errorf("lat = %f, want 69.6496", lat)
	}
	if lon != 18.956 {
		t.Errorf("lon = %f, want 18.956", lon)
	}
	if name != "Tromsø, Tromsø" {
		t.Errorf("name = %q, want %q", name, "Tromsø, Tromsø")
	}

	// Second call should hit cache
	lat2, lon2, name2, err := ws.geocode(context.Background(), "Tromsø")
	if err != nil {
		t.Fatalf("cached geocode returned error: %v", err)
	}
	if lat2 != lat || lon2 != lon || name2 != name {
		t.Error("cached geocode returned different values")
	}
}

func TestGeocodeNorwegianCharacters(t *testing.T) {
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := NewWeatherServer()
	ws.geoBaseURL = geo.URL

	tests := []struct {
		name    string
		wantLat float64
		wantLon float64
		wantIn  string
	}{
		{"Haugastøl", 60.512, 7.8631, "Hol"},
		{"Tromsø", 69.6496, 18.956, "Tromsø"},
		{"Ålesund", 62.4722, 6.1549, "Ålesund"},
		{"Ærøy", 59.0167, 5.7833, "Stavanger"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lat, lon, displayName, err := ws.geocode(context.Background(), tt.name)
			if err != nil {
				t.Fatalf("geocode(%q) failed: %v", tt.name, err)
			}
			if lat != tt.wantLat {
				t.Errorf("lat = %f, want %f", lat, tt.wantLat)
			}
			if lon != tt.wantLon {
				t.Errorf("lon = %f, want %f", lon, tt.wantLon)
			}
			if !contains(displayName, tt.wantIn) {
				t.Errorf("displayName = %q, want it to contain %q", displayName, tt.wantIn)
			}
		})
	}
}

func TestGeocodeNotFound(t *testing.T) {
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := NewWeatherServer()
	ws.geoBaseURL = geo.URL

	_, _, _, err := ws.geocode(context.Background(), "Nonexistent")
	if err == nil {
		t.Error("geocode should fail for unknown location")
	}
}

func TestEndToEndGeocodeThenForecast(t *testing.T) {
	mock := newMockMetServer(t)
	defer mock.Close()
	geo := newMockGeoServer(t)
	defer geo.Close()

	ws := newTestWeatherServer(t, mock.URL, geo.URL)

	// Geocode resolves correctly
	lat, lon, name, err := ws.geocode(context.Background(), "Oslo")
	if err != nil {
		t.Fatalf("geocode failed: %v", err)
	}
	if name != "Oslo, Oslo" {
		t.Errorf("geocode name = %q, want %q", name, "Oslo, Oslo")
	}

	// Weather fetch works
	data, err := ws.fetchWeather(context.Background(), lat, lon)
	if err != nil {
		t.Fatalf("fetchWeather failed: %v", err)
	}
	if len(data.Properties.Timeseries) == 0 {
		t.Fatal("expected timeseries data")
	}

	// Full tool call: get_forecast with place name goes through geocode + weather + formatting
	result := callTool(t, ws.handleGetForecast, map[string]interface{}{
		"location": "Oslo",
	})
	if result.IsError {
		t.Fatalf("forecast tool failed: %v", result.Content)
	}
	text := getResultText(t, result)
	if !contains(text, "Oslo") {
		t.Errorf("forecast should mention Oslo, got: %s", text)
	}
}

func TestEndToEndCoordinateInput(t *testing.T) {
	mock := newMockMetServer(t)
	defer mock.Close()

	ws := newTestWeatherServer(t, mock.URL, "")

	// Coordinate input bypasses geocoding entirely
	result := callTool(t, ws.handleGetPrecipitation, map[string]interface{}{
		"location": "60.39, 5.32",
		"period":   "today",
	})
	if result.IsError {
		t.Fatalf("precipitation tool failed: %v", result.Content)
	}
	text := getResultText(t, result)
	if !contains(text, "Total:") {
		t.Errorf("precipitation should contain Total, got: %s", text)
	}
}

// helpers

func fetchMockData(t *testing.T, url string) *MetResponse {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("mock fetch failed: %v", err)
	}
	defer resp.Body.Close()
	var data MetResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("mock decode failed: %v", err)
	}
	return &data
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
