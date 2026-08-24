package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func init() {
	if os.Getenv("APP_ENV") != "production" {
		err := godotenv.Load(".env")
		if err != nil {
			log.Println("No se pudo cargar el archivo .env, usando variables del sistema")
		}
	}
}

func main() {
	r := gin.Default()
	InitServer(r) // Como ya están en el mismo paquete, se pueden llamar directamente
}
