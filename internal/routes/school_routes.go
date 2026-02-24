package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerSchoolRoutes(api *gin.RouterGroup, deps Dependencies) {
	schoolService := services.NewSchoolService(deps.MongoClient, deps.DBName)
	schoolHandler := handlers.NewSchoolHandler(schoolService)

	schools := api.Group("/schools")
	{
		schools.POST("", schoolHandler.CreateSchool)
		schools.GET("", schoolHandler.ListSchools)
		schools.GET("/:id", schoolHandler.GetSchool)
		schools.PUT("/:id", schoolHandler.UpdateSchool)
		schools.DELETE("/:id", schoolHandler.DeleteSchool)
	}
}
