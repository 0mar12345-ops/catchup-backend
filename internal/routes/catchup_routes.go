package routes

import (
	"github.com/0mar12345-ops/internal/handlers"
	"github.com/0mar12345-ops/internal/middleware"
	"github.com/0mar12345-ops/internal/services"
	"github.com/gin-gonic/gin"
)

func registerCatchUpRoutes(api *gin.RouterGroup, deps Dependencies) {
	// Create UserOAuthService to share across catchup services
	userOAuthService := services.NewUserOAuthService(
		deps.GoogleClientID,
		deps.GoogleClientSecret,
		deps.GoogleRedirectURL,
		deps.GoogleOAuthState,
		deps.FrontendURL,
		deps.MongoClient,
		deps.DBName,
	)

	catchUpService := services.NewCatchUpService(deps.MongoClient, deps.DBName, deps.Config, userOAuthService)
	catchUpHandler := handlers.NewCatchUpHandler(catchUpService)

	catchUpViewService := services.NewCatchUpViewService(deps.MongoClient, deps.DBName, deps.Config, userOAuthService)
	catchUpViewHandler := handlers.NewCatchUpViewHandler(catchUpViewService)

	authGuard := middleware.NewAuthGuard(deps.JWTSecret, deps.JWTCookieName)

	catchup := api.Group("/catchup")
	catchup.Use(authGuard.RequireAuth())
	{
		catchup.POST("/generate", catchUpHandler.GenerateCatchUp)
		catchup.GET("/course/:courseId/stats", catchUpViewHandler.GetCourseStats)
		catchup.GET("/course/:courseId/student/:studentId/lessons", catchUpViewHandler.GetStudentCatchUpLessons)
		catchup.GET("/course/:courseId/student/:studentId", catchUpViewHandler.GetCatchUpLesson)
		catchup.GET("/lesson/:lessonId", catchUpViewHandler.GetCatchUpLessonById)
		catchup.POST("/lesson/:lessonId/deliver", catchUpViewHandler.DeliverCatchUpLesson)
		catchup.POST("/lesson/:lessonId/regenerate", catchUpViewHandler.RegenerateCatchUpLesson)
	}
}
