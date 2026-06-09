package services

import (
	"context"
	"fmt"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// --- Response DTOs ---

type AdminOverviewResponse struct {
	TotalStudents     int                    `json:"total_students"`
	AbsencesThisMonth int                    `json:"absences_this_month"`
	CatchUp           CatchUpOverviewStats   `json:"catch_up"`
	Behaviour         BehaviourOverviewStats `json:"behaviour"`
	RecentAbsences    []RecentAbsenceItem    `json:"recent_absences"`
	CourseStats       []CourseSummaryItem    `json:"course_stats"`
	MissedAssessments []MissedAssessmentItem `json:"missed_assessments"`
}

type CatchUpOverviewStats struct {
	TotalGenerated int `json:"total_generated"`
	TotalCompleted int `json:"total_completed"`
}

type BehaviourOverviewStats struct {
	Total    int `json:"total"`
	Positive int `json:"positive"`
	Negative int `json:"negative"`
}

type RecentAbsenceItem struct {
	StudentName string    `json:"student_name"`
	CourseName  string    `json:"course_name"`
	Date        time.Time `json:"date"`
	Reason      string    `json:"reason"`  // not stored in AbsenceRecord; always empty
	Excused     bool      `json:"excused"` // not stored in AbsenceRecord; always false
}

type CourseSummaryItem struct {
	CourseID          string `json:"course_id"`
	CourseName        string `json:"course_name"`
	CatchUpGenerated  int    `json:"catch_up_generated"`
	CatchUpCompleted  int    `json:"catch_up_completed"`
	BehaviourTotal    int    `json:"behaviour_total"`
	BehaviourPositive int    `json:"behaviour_positive"`
	BehaviourNegative int    `json:"behaviour_negative"`
}

type MissedAssessmentItem struct {
	StudentName    string    `json:"student_name"`
	CourseName     string    `json:"course_name"`
	AbsentOn       time.Time `json:"absent_on"`
	WeekNumber     int       `json:"week_number"`
	AssessmentInfo string    `json:"assessment_info"`
}

// --- Service ---

type AdminOverviewService struct {
	db *mongo.Database
}

func NewAdminOverviewService(client *mongo.Client, dbName string) *AdminOverviewService {
	return &AdminOverviewService{db: client.Database(dbName)}
}

func (s *AdminOverviewService) GetOverview(ctx context.Context, teacherID, schoolID string) (*AdminOverviewResponse, error) {
	teacherOID, err := bson.ObjectIDFromHex(teacherID)
	if err != nil {
		return nil, fmt.Errorf("invalid teacher id")
	}
	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, fmt.Errorf("invalid school id")
	}

	resp := &AdminOverviewResponse{
		RecentAbsences:    []RecentAbsenceItem{},
		CourseStats:       []CourseSummaryItem{},
		MissedAssessments: []MissedAssessmentItem{},
	}

	courses, err := s.getTeacherCourses(ctx, teacherOID, schoolOID)
	if err != nil {
		return nil, err
	}
	courseIDs := make([]bson.ObjectID, len(courses))
	courseNameMap := make(map[bson.ObjectID]string, len(courses))
	for i, c := range courses {
		courseIDs[i] = c.ID
		courseNameMap[c.ID] = c.Name
	}

	// 1. Total active students in school
	if n, countErr := s.db.Collection("students").CountDocuments(ctx, bson.M{
		"school_id": schoolOID, "is_active": true,
	}); countErr == nil {
		resp.TotalStudents = int(n)
	}

	// 2. Absences this calendar month across the school
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	if n, countErr := s.db.Collection("absence_records").CountDocuments(ctx, bson.M{
		"school_id": schoolOID,
		"absent_on": bson.M{"$gte": monthStart, "$lt": monthEnd},
	}); countErr == nil {
		resp.AbsencesThisMonth = int(n)
	}

	// 3. Catch-up stats for teacher's school
	generatedStatuses := bson.A{
		string(models.CatchUpStatusGenerated),
		string(models.CatchUpStatusDelivered),
		string(models.CatchUpStatusCompleted),
	}
	generated, _ := s.db.Collection("catchup_lessons").CountDocuments(ctx, bson.M{
		"school_id": schoolOID,
		"status":    bson.M{"$in": generatedStatuses},
	})
	completed, _ := s.db.Collection("catchup_lessons").CountDocuments(ctx, bson.M{
		"school_id": schoolOID,
		"status":    string(models.CatchUpStatusCompleted),
	})
	resp.CatchUp = CatchUpOverviewStats{
		TotalGenerated: int(generated),
		TotalCompleted: int(completed),
	}

	// 4. Behaviour stats (teacher-scoped)
	bTotal, _ := s.db.Collection("behaviour_logs").CountDocuments(ctx, bson.M{
		"school_id": schoolOID, "teacher_id": teacherOID,
	})
	bPositive, _ := s.db.Collection("behaviour_logs").CountDocuments(ctx, bson.M{
		"school_id": schoolOID, "teacher_id": teacherOID,
		"type": string(models.BehaviourTypePositive),
	})
	bNegative, _ := s.db.Collection("behaviour_logs").CountDocuments(ctx, bson.M{
		"school_id": schoolOID, "teacher_id": teacherOID,
		"type": string(models.BehaviourTypeNegative),
	})
	resp.Behaviour = BehaviourOverviewStats{
		Total:    int(bTotal),
		Positive: int(bPositive),
		Negative: int(bNegative),
	}

	// 5. Recent absences (last 20, school-wide, with student/course names)
	resp.RecentAbsences, _ = s.getRecentAbsences(ctx, schoolOID, courseNameMap)

	// 6 & 7. Per-course catch-up and behaviour stats
	resp.CourseStats = s.buildCourseStats(ctx, courses, courseIDs, schoolOID, teacherOID)

	// 8. Missed assessments (absences on assessment weeks from term overview)
	if len(courseIDs) > 0 {
		resp.MissedAssessments, _ = s.getMissedAssessments(ctx, teacherOID, schoolOID, courseIDs, courseNameMap)
	}

	return resp, nil
}

func (s *AdminOverviewService) getTeacherCourses(ctx context.Context, teacherOID, schoolOID bson.ObjectID) ([]models.Course, error) {
	cursor, err := s.db.Collection("courses").Find(ctx, bson.M{
		"teacher_id":  teacherOID,
		"school_id":   schoolOID,
		"is_archived": bson.M{"$ne": true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load courses: %w", err)
	}
	defer cursor.Close(ctx)
	var courses []models.Course
	if err := cursor.All(ctx, &courses); err != nil {
		return nil, err
	}
	return courses, nil
}

func (s *AdminOverviewService) studentNameMap(ctx context.Context, ids []bson.ObjectID) map[bson.ObjectID]string {
	m := make(map[bson.ObjectID]string, len(ids))
	if len(ids) == 0 {
		return m
	}
	cursor, err := s.db.Collection("students").Find(ctx,
		bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"_id": 1, "name": 1}),
	)
	if err != nil {
		return m
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var st struct {
			ID   bson.ObjectID `bson:"_id"`
			Name string        `bson:"name"`
		}
		if cursor.Decode(&st) == nil {
			m[st.ID] = st.Name
		}
	}
	return m
}

func (s *AdminOverviewService) getRecentAbsences(ctx context.Context, schoolOID bson.ObjectID, courseNameMap map[bson.ObjectID]string) ([]RecentAbsenceItem, error) {
	cursor, err := s.db.Collection("absence_records").Find(ctx,
		bson.M{"school_id": schoolOID},
		options.Find().
			SetSort(bson.D{{Key: "absent_on", Value: -1}}).
			SetLimit(20),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var records []models.AbsenceRecord
	if err := cursor.All(ctx, &records); err != nil {
		return nil, err
	}

	studentIDs := uniqueOIDs(records, func(r models.AbsenceRecord) bson.ObjectID { return r.StudentID })
	students := s.studentNameMap(ctx, studentIDs)

	items := make([]RecentAbsenceItem, 0, len(records))
	for _, r := range records {
		items = append(items, RecentAbsenceItem{
			StudentName: students[r.StudentID],
			CourseName:  courseNameMap[r.CourseID],
			Date:        r.AbsentOn,
		})
	}
	return items, nil
}

type catchupCourseAgg struct {
	CourseID  bson.ObjectID `bson:"_id"`
	Generated int           `bson:"generated"`
	Completed int           `bson:"completed"`
}

func (s *AdminOverviewService) catchUpStatsByCourse(ctx context.Context, schoolOID bson.ObjectID, courseIDs []bson.ObjectID) map[bson.ObjectID]catchupCourseAgg {
	result := make(map[bson.ObjectID]catchupCourseAgg)
	generatedStatuses := bson.A{
		string(models.CatchUpStatusGenerated),
		string(models.CatchUpStatusDelivered),
		string(models.CatchUpStatusCompleted),
	}
	pipeline := bson.A{
		bson.M{"$match": bson.M{
			"school_id": schoolOID,
			"course_id": bson.M{"$in": courseIDs},
		}},
		bson.M{"$group": bson.M{
			"_id": "$course_id",
			"generated": bson.M{"$sum": bson.M{
				"$cond": bson.A{
					bson.M{"$in": bson.A{"$status", generatedStatuses}},
					1, 0,
				},
			}},
			"completed": bson.M{"$sum": bson.M{
				"$cond": bson.A{
					bson.M{"$eq": bson.A{"$status", string(models.CatchUpStatusCompleted)}},
					1, 0,
				},
			}},
		}},
	}
	cursor, err := s.db.Collection("catchup_lessons").Aggregate(ctx, pipeline)
	if err != nil {
		return result
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var agg catchupCourseAgg
		if cursor.Decode(&agg) == nil {
			result[agg.CourseID] = agg
		}
	}
	return result
}

type behaviourCourseAgg struct {
	CourseID  bson.ObjectID `bson:"_id"`
	Total     int           `bson:"total"`
	Positive  int           `bson:"positive"`
	Negative  int           `bson:"negative"`
}

func (s *AdminOverviewService) behaviourStatsByCourse(ctx context.Context, schoolOID, teacherOID bson.ObjectID, courseIDs []bson.ObjectID) map[bson.ObjectID]behaviourCourseAgg {
	result := make(map[bson.ObjectID]behaviourCourseAgg)
	pipeline := bson.A{
		bson.M{"$match": bson.M{
			"school_id":  schoolOID,
			"teacher_id": teacherOID,
			"course_id":  bson.M{"$in": courseIDs},
		}},
		bson.M{"$group": bson.M{
			"_id":   "$course_id",
			"total": bson.M{"$sum": 1},
			"positive": bson.M{"$sum": bson.M{
				"$cond": bson.A{
					bson.M{"$eq": bson.A{"$type", string(models.BehaviourTypePositive)}},
					1, 0,
				},
			}},
			"negative": bson.M{"$sum": bson.M{
				"$cond": bson.A{
					bson.M{"$eq": bson.A{"$type", string(models.BehaviourTypeNegative)}},
					1, 0,
				},
			}},
		}},
	}
	cursor, err := s.db.Collection("behaviour_logs").Aggregate(ctx, pipeline)
	if err != nil {
		return result
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var agg behaviourCourseAgg
		if cursor.Decode(&agg) == nil {
			result[agg.CourseID] = agg
		}
	}
	return result
}

func (s *AdminOverviewService) buildCourseStats(
	ctx context.Context,
	courses []models.Course,
	courseIDs []bson.ObjectID,
	schoolOID, teacherOID bson.ObjectID,
) []CourseSummaryItem {
	if len(courses) == 0 {
		return []CourseSummaryItem{}
	}
	catchupByC := s.catchUpStatsByCourse(ctx, schoolOID, courseIDs)
	behaviourByC := s.behaviourStatsByCourse(ctx, schoolOID, teacherOID, courseIDs)

	items := make([]CourseSummaryItem, 0, len(courses))
	for _, c := range courses {
		cu := catchupByC[c.ID]
		bh := behaviourByC[c.ID]
		items = append(items, CourseSummaryItem{
			CourseID:          c.ID.Hex(),
			CourseName:        c.Name,
			CatchUpGenerated:  cu.Generated,
			CatchUpCompleted:  cu.Completed,
			BehaviourTotal:    bh.Total,
			BehaviourPositive: bh.Positive,
			BehaviourNegative: bh.Negative,
		})
	}
	return items
}

func (s *AdminOverviewService) getMissedAssessments(
	ctx context.Context,
	teacherOID, schoolOID bson.ObjectID,
	courseIDs []bson.ObjectID,
	courseNameMap map[bson.ObjectID]string,
) ([]MissedAssessmentItem, error) {
	// Most recent term overview for this teacher.
	var termOverview models.TermOverviewUpload
	err := s.db.Collection("term_overview_uploads").FindOne(ctx,
		bson.M{"teacher_id": teacherOID, "school_id": schoolOID},
		options.FindOne().SetSort(bson.D{{Key: "uploaded_at", Value: -1}}),
	).Decode(&termOverview)
	if err != nil {
		return []MissedAssessmentItem{}, nil
	}

	assessmentWeeks := make(map[int]string)
	for _, entry := range termOverview.Entries {
		if entry.AssessmentYN {
			desc := entry.Assessment
			if desc == "" {
				desc = entry.TopicTaught + " – assessment"
			}
			assessmentWeeks[entry.WeekNumber] = desc
		}
	}
	if len(assessmentWeeks) == 0 {
		return []MissedAssessmentItem{}, nil
	}

	cursor, err := s.db.Collection("absence_records").Find(ctx,
		bson.M{"school_id": schoolOID, "course_id": bson.M{"$in": courseIDs}},
		options.Find().SetSort(bson.D{{Key: "absent_on", Value: -1}}).SetLimit(500),
	)
	if err != nil {
		return []MissedAssessmentItem{}, nil
	}
	defer cursor.Close(ctx)

	var absences []models.AbsenceRecord
	if err := cursor.All(ctx, &absences); err != nil {
		return []MissedAssessmentItem{}, nil
	}

	studentIDs := uniqueOIDs(absences, func(r models.AbsenceRecord) bson.ObjectID { return r.StudentID })
	students := s.studentNameMap(ctx, studentIDs)

	var items []MissedAssessmentItem
	for _, ab := range absences {
		_, isoWeek := ab.AbsentOn.ISOWeek()
		assessmentDesc, hasAssessment := assessmentWeeks[isoWeek]
		if !hasAssessment {
			continue
		}
		items = append(items, MissedAssessmentItem{
			StudentName:    students[ab.StudentID],
			CourseName:     courseNameMap[ab.CourseID],
			AbsentOn:       ab.AbsentOn,
			WeekNumber:     isoWeek,
			AssessmentInfo: assessmentDesc,
		})
	}
	if items == nil {
		return []MissedAssessmentItem{}, nil
	}
	return items, nil
}

func uniqueOIDs[T any](slice []T, extract func(T) bson.ObjectID) []bson.ObjectID {
	seen := make(map[bson.ObjectID]struct{})
	var ids []bson.ObjectID
	for _, item := range slice {
		id := extract(item)
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}
