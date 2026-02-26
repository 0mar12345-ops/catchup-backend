package services

import (
	"context"
	"errors"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrInvalidCourseUserID   = errors.New("invalid auth user id")
	ErrInvalidCourseSchoolID = errors.New("invalid auth school id")
	ErrInvalidCourseID       = errors.New("invalid course id")
	ErrCourseNotFound        = errors.New("course not found")
)

type CourseDashboardItem struct {
	ID            bson.ObjectID `json:"id"`
	Name          string        `json:"name"`
	Section       string        `json:"section,omitempty"`
	Subject       string        `json:"subject,omitempty"`
	Room          string        `json:"room,omitempty"`
	GradeLevel    string        `json:"grade_level,omitempty"`
	Source        string        `json:"source"`
	IsArchived    bool          `json:"is_archived"`
	TotalStudents int64         `json:"total_students"`
}

type CourseStudent struct {
	ID       bson.ObjectID `json:"id"`
	Name     string        `json:"name"`
	Email    string        `json:"email"`
	PhotoURL string        `json:"photo_url,omitempty"`
}

type CourseWithStudents struct {
	ID            bson.ObjectID   `json:"id"`
	Name          string          `json:"name"`
	Section       string          `json:"section,omitempty"`
	Subject       string          `json:"subject,omitempty"`
	Room          string          `json:"room,omitempty"`
	GradeLevel    string          `json:"grade_level,omitempty"`
	Source        string          `json:"source"`
	IsArchived    bool            `json:"is_archived"`
	TotalStudents int             `json:"total_students"`
	Students      []CourseStudent `json:"students"`
}

type CourseService struct {
	coursesCollection     *mongo.Collection
	enrollmentsCollection *mongo.Collection
	studentsCollection    *mongo.Collection
}

func NewCourseService(client *mongo.Client, dbName string) *CourseService {
	db := client.Database(dbName)

	return &CourseService{
		coursesCollection:     db.Collection("courses"),
		enrollmentsCollection: db.Collection("enrollments"),
		studentsCollection:    db.Collection("students"),
	}
}

func (s *CourseService) ListDashboardCourses(ctx context.Context, userID, schoolID string) ([]CourseDashboardItem, error) {
	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, ErrInvalidCourseUserID
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, ErrInvalidCourseSchoolID
	}

	cursor, err := s.coursesCollection.Find(ctx, bson.M{
		"school_id":  schoolOID,
		"teacher_id": userOID,
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	courses := make([]models.Course, 0)
	if err := cursor.All(ctx, &courses); err != nil {
		return nil, err
	}

	items := make([]CourseDashboardItem, 0, len(courses))
	for _, course := range courses {
		totalStudents, err := s.enrollmentsCollection.CountDocuments(ctx, bson.M{
			"school_id": schoolOID,
			"course_id": course.ID,
			"status":    models.EnrollmentActive,
		})
		if err != nil {
			totalStudents = int64(course.StudentCount)
		}

		items = append(items, CourseDashboardItem{
			ID:            course.ID,
			Name:          course.Name,
			Section:       course.Section,
			Subject:       course.Subject,
			Room:          course.Room,
			GradeLevel:    course.GradeLevel,
			Source:        string(course.Source),
			IsArchived:    course.IsArchived,
			TotalStudents: totalStudents,
		})
	}

	return items, nil
}

func (s *CourseService) GetCourseWithStudents(ctx context.Context, courseID, userID, schoolID string) (*CourseWithStudents, error) {
	courseOID, err := bson.ObjectIDFromHex(courseID)
	if err != nil {
		return nil, ErrInvalidCourseID
	}

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, ErrInvalidCourseUserID
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, ErrInvalidCourseSchoolID
	}

	// Get the course
	var course models.Course
	err = s.coursesCollection.FindOne(ctx, bson.M{
		"_id":        courseOID,
		"school_id":  schoolOID,
		"teacher_id": userOID,
	}).Decode(&course)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCourseNotFound
		}
		return nil, err
	}

	// Get enrolled students
	cursor, err := s.enrollmentsCollection.Find(ctx, bson.M{
		"school_id": schoolOID,
		"course_id": courseOID,
		"status":    models.EnrollmentActive,
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	enrollments := make([]models.Enrollment, 0)
	if err := cursor.All(ctx, &enrollments); err != nil {
		return nil, err
	}

	// Get student details
	studentIDs := make([]bson.ObjectID, 0, len(enrollments))
	for _, e := range enrollments {
		studentIDs = append(studentIDs, e.StudentID)
	}

	students := make([]CourseStudent, 0)
	if len(studentIDs) > 0 {
		studentCursor, err := s.studentsCollection.Find(ctx, bson.M{
			"_id": bson.M{"$in": studentIDs},
		})
		if err != nil {
			return nil, err
		}
		defer studentCursor.Close(ctx)

		var studentModels []models.Student
		if err := studentCursor.All(ctx, &studentModels); err != nil {
			return nil, err
		}

		for _, s := range studentModels {
			students = append(students, CourseStudent{
				ID:    s.ID,
				Name:  s.Name,
				Email: s.Email,
			})
		}
	}

	return &CourseWithStudents{
		ID:            course.ID,
		Name:          course.Name,
		Section:       course.Section,
		Subject:       course.Subject,
		Room:          course.Room,
		GradeLevel:    course.GradeLevel,
		Source:        string(course.Source),
		IsArchived:    course.IsArchived,
		TotalStudents: len(students),
		Students:      students,
	}, nil
}
