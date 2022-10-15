package routes

import (
	"os"
	"strconv"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/lekan-pvp/shorten-url-fiber-redis/api/database"
	"github.com/lekan-pvp/shorten-url-fiber-redis/api/helpers"
)

type request struct {
	URL string `json:"url"`
	CustomShort string `json:"short"`
	Expiry time.Duration `json:"expity"`
}

type response struct {
	URL string `json:"url"`
	CustomShort string `json:"short"`
	Expiry time.Duration `json:"expiry"`
	XRateRemaining int `json:"rate_limit"`
	XRateLimitReset time.Duration `json:"rate_limit_res"`
}

func ShortenURL(c *fiber.Ctx) error {
	body := new(request)

	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cannot parse JSON"})
	}

	// implementing rate limiting 
	r2 := database.CreateClient(1)
	defer r2.Close()

	val, err := r2.Get(database.Ctx, c.IP()).Result()
	if err == redis.Nil {
		_ = r2.Set(database.Ctx, c.IP(), os.Getenv("API_QUOTA"), 30*60*time.Second) 
	} else {
		val, err = r2.Get(database.Ctx, c.IP()).Result()
		if err != nil {
			return err
		}

		valInt, err := strconv.Atoi(val)
		if err != nil {
			return err
		}

		if valInt <= 0 {
			limit, err := r2.TTL(database.Ctx, c.IP()).Result()
			if err != nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
					"error": "Rate limit exceeded",
					"rate_limit_reset": limit / time.Nanosecond / time.Minute,
				})
			}
			
		}
	}


	// check if the input if an actual URL
	if !govalidator.IsURL(body.URL) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid URL"})
	}

	// check for domain error 
	if !helpers.RemoveDomainError(body.URL) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": ""})
	}

	// enforce https, SSL
	body.URL = helpers.EnforceHTTP(body.URL)

	var id string

	if body.CustomShort == "" {
		id = uuid.New().String()[:6]
	} else {
		id = body.CustomShort
	}

	r := database.CreateClient(0)
	defer r.Close()

	val, err = r.Get(database.Ctx, id).Result()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "URL short not found in database",
		})
	}
	if val != "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "URL custom short already in use",
		})
	}

	if body.Expiry == 0 {
		body.Expiry = 24
	}

	err = r.Set(database.Ctx, id, body.URL, body.Expiry*3600*time.Second).Err()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Unable to connect to server",
		})
	}

	resp := response{
		URL: body.URL,
		CustomShort: "",
		Expiry: body.Expiry,
		XRateRemaining: 10,
		XRateLimitReset: 30,
	}

	r2.Decr(database.Ctx, c.IP())

	val, err = r2.Get(database.Ctx, c.IP()).Result()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "URL short not found in database",
		})
	}

	resp.XRateRemaining, err = strconv.Atoi(val)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Cannot convert value to int",
		})
	}

	ttl, err := r2.TTL(database.Ctx, c.IP()).Result()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "cannot to get ttl from database",
		})
	}
	resp.XRateLimitReset = ttl / time.Nanosecond / time.Minute

	resp.CustomShort = os.Getenv("DOMAIN") + "/" + id

	return c.Status(fiber.StatusOK).JSON(resp)
}