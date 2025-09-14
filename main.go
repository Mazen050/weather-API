package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/ulule/limiter/v3"
	ginlimiter "github.com/ulule/limiter/v3/drivers/middleware/gin"
	memory "github.com/ulule/limiter/v3/drivers/store/memory"
)

var (
	apiKey       string
	redisURL     string
	redisAPIToken string
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		fmt.Println("No .env file found")
	}

	apiKey = os.Getenv("VISUAL_CROSSING_API_KEY")
	redisURL = os.Getenv("UPSTASH_REDIS_URL")
	redisAPIToken = os.Getenv("UPSTASH_REDIS_TOKEN")

	if apiKey == "" || redisURL == "" || redisAPIToken == "" {
		panic("Missing .env values")
	}

	// Setup Gin router
	r := gin.Default()

	// Rate limiting: 10 req per minute
	rate, _ := limiter.NewRateFromFormatted("10-M")
	store := memory.NewStore()
	r.Use(ginlimiter.NewMiddleware(limiter.New(store, rate)))

	r.GET("/weather/:city", getWeather)

	r.Run(":51000")
}

func getWeather(c *gin.Context) {
	city := c.Param("city")

	// Try getting from cache
	if cached, err := redisGet(city); err == nil && cached != "" {
		c.Data(http.StatusOK, "application/json", []byte(cached))
		return
	}

	// Not cached → fetch from Visual Crossing
	url := fmt.Sprintf(
		"https://weather.visualcrossing.com/VisualCrossingWebServices/rest/services/timeline/%s?unitGroup=metric&key=%s&contentType=json",
		city, apiKey,
	)

	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch weather data"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Cache for 12 hours
	_ = redisSet(city, body, 12*time.Hour)

	// Return response
	var parsed map[string]interface{}
	json.Unmarshal(body, &parsed)
	c.JSON(http.StatusOK, parsed)
}

// --- Upstash Redis REST helpers ---

func redisGet(key string) (string, error) {
	req, _ := http.NewRequest("GET", redisURL+"/get/"+key, nil)
	req.Header.Set("Authorization", "Bearer "+redisAPIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Result, nil
}

func redisSet(key string, value []byte, ttl time.Duration) error {
	// POST https://<url>/set/<key>?EX=<seconds>&value=<value>
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("%s/set/%s?EX=%d&value=%s", redisURL, key, int(ttl.Seconds()), value),
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+redisAPIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
