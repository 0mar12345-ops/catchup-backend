package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type LessonBuilderService struct {
	coursesCollection *mongo.Collection
}

func NewLessonBuilderService(client *mongo.Client, dbName string) *LessonBuilderService {
	return &LessonBuilderService{
		coursesCollection: client.Database(dbName).Collection("courses"),
	}
}

func (s *LessonBuilderService) GenerateLesson(ctx context.Context, teacherID, schoolID, courseID string, weekNumber int, dateText, topic string) (map[string]any, error) {
	courseOID, err := bson.ObjectIDFromHex(courseID)
	if err != nil {
		return nil, fmt.Errorf("invalid course id")
	}

	teacherOID, err := bson.ObjectIDFromHex(teacherID)
	if err != nil {
		return nil, fmt.Errorf("invalid teacher id")
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, fmt.Errorf("invalid school id")
	}

	var course models.Course
	if err := s.coursesCollection.FindOne(ctx, bson.M{"_id": courseOID, "teacher_id": teacherOID, "school_id": schoolOID}).Decode(&course); err != nil {
		return nil, fmt.Errorf("course not found")
	}

	weekLabel := fmt.Sprintf("Week %d", weekNumber)
	if weekNumber <= 0 {
		weekLabel = "Current week"
	}
	if strings.TrimSpace(dateText) != "" {
		if parsed, err := time.Parse("2006-01-02", dateText); err == nil {
			weekLabel = parsed.Format("02 Jan 2006")
		}
	}

	topicLabel := strings.TrimSpace(topic)
	if topicLabel == "" {
		topicLabel = "Class recap"
	}

	return map[string]any{
		"course_id":   course.ID.Hex(),
		"course_name": course.Name,
		"week_label":  weekLabel,
		"topic":       topicLabel,
		"lesson_plan": map[string]string{
			"learning_objectives":    fmt.Sprintf("Students will be able to explain the key ideas behind %s and apply them to one worked example.", topicLabel),
			"starter_activity":       fmt.Sprintf("Begin with a 5-minute retrieval warm-up on %s and ask learners to share one prior fact they remember.", topicLabel),
			"main_teaching_sequence": fmt.Sprintf("Model the concept of %s with a short example, then guide paired practice, and finish with a mini-check for understanding.", topicLabel),
			"practice_questions":     fmt.Sprintf("1. What is one important idea in %s?\n2. Explain how the example supports your answer.\n3. Solve one challenge question independently.", topicLabel),
			"exit_ticket":            fmt.Sprintf("Write one sentence explaining what you learned about %s and one question you still have.", topicLabel),
		},
	}, nil
}
