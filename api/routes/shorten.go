package routes

import (
	"os"
	"shorten-url/database"
	"shorten-url/helpers"
	"strconv"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type request struct {
	URL  		 string         `json:"url"`
	CustomShort  string         `json:"short"`
	Expiry       time.Duration  `json:"expiry"`
}//this is the format of the request body that we will receive from the client when they want to shorten a URL. It will contain the original URL

type response struct {
	URL                string			`json:"url"`
	CustomShort        string			`json:"short"`
	Expiry             time.Duration	`json:"expiry"`
	XRateRemaining     int				`json:"rate_limit"`
	XRateLimitReset    time.Duration	`json:"rate_limit_reset"`
}//this is the format of the response body that we will send back to the client after we have shortened the URL. It will contain the shortened URL and the original URL

func ShortenURL(c *fiber.Ctx) error {
	body := new(request)

	if err := c.BodyParser(&body); err!=nil{
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error":"Invalid URL"})
	}

	//implement the rate limiting 
	r2 := database.CreateClient(1)//here we are making a database connection 
	defer r2.Close()//defer means that this function will be executed at the end of the call stack as we dont want to keep the database connection open 
	val,err := r2.Get(database.Ctx,c.IP()).Result()
	if err == redis.Nil{
		r2.Set(database.Ctx,c.IP(),os.Getenv("API_QUOTA"),30*60*time.Second).Err()
	}else{
		// val,_ = r2.Get(database.Ctx,c.IP()).Result()
		valInt,_ := strconv.Atoi(val)
		if valInt<=0{
			limit, _ := r2.TTL(database.Ctx,c.IP()).Result()
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error":"rate limit ecxeeded",
				"reset time remaiming": limit/time.Nanosecond/time.Minute,
			})
		}
	}

	//check if the input is actual url  
	if !govalidator.IsURL(body.URL){
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error":"Invalid URL"})
	}

	//checking for the domain errors like if user is not sending the localhost:3000 as this may lead to an infinite loop 
	if !helpers.RemoveDomainError(body.URL){
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error":"cannot shorten this url"})
	}

	//enforce http and ssl
	body.URL = helpers.EnforceHTTP(body.URL)

	var id string

	if body.CustomShort == ""{
		id = uuid.New().String()[:6]
	} else {
		id = body.CustomShort
	}

	r := database.CreateClient(0)
	defer r.Close()

	val,_ = r.Get(database.Ctx,id).Result()

	if val!=""{
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":"URL custom short is already in use",
		})
	}

	if body.Expiry==0{// 1 day ka ttl
		body.Expiry=24
	}

	err = r.Set(database.Ctx,id,body.URL,body.Expiry*3600*time.Second).Err()

	if err!=nil{
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":"Unable to connect to the server",
		})
	}

	resp := response{
		URL: body.URL,
		CustomShort: "",
		Expiry: body.Expiry,
		XRateRemaining: 10,
		XRateLimitReset: 30,
	}

	r2.Decr(database.Ctx,c.IP()) 

	val,_ = r2.Get(database.Ctx,c.IP()).Result()
	resp.XRateRemaining,_ = strconv.Atoi(val)

	ttl,_ := r2.TTL(database.Ctx,c.IP()).Result()
	resp.XRateLimitReset = ttl/time.Nanosecond/time.Minute 

	resp.CustomShort = os.Getenv("DOMAIN") + "/" + id

	return c.Status(fiber.StatusOK).JSON(resp)
	
}