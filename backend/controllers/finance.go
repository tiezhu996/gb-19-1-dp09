package controllers

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"edu-train/database"
	"edu-train/models"
	"edu-train/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockForUpdate 加行锁防止并发读写不一致；SQLite 不支持 SELECT ... FOR UPDATE，跳过（其单写者事务已串行化）
func lockForUpdate(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() == "sqlite" {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE"})
}

func GetPayments(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	studentID := c.Query("student_id")
	paymentMethod := c.Query("payment_method")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	typeParam := c.Query("type")

	offset := (page - 1) * pageSize

	query := database.DB.Model(&models.Payment{}).Preload("Student").Preload("Course")

	if studentID != "" {
		query = query.Where("student_id = ?", studentID)
	}

	if paymentMethod != "" {
		query = query.Where("payment_method = ?", paymentMethod)
	}

	if startDate != "" {
		query = query.Where("payment_date >= ?", startDate)
	}

	if endDate != "" {
		query = query.Where("payment_date <= ?", endDate)
	}

	if typeParam != "" {
		query = query.Where("type = ?", typeParam)
	}

	var total int64
	query.Count(&total)

	var payments []models.Payment
	if err := query.Order("payment_date DESC, created_at DESC").Offset(offset).Limit(pageSize).Find(&payments).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, gin.H{
		"list":  payments,
		"total": total,
		"page":  page,
		"page_size": pageSize,
	})
}

func GetPayment(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var payment models.Payment
	if err := database.DB.Preload("Student").Preload("Course").First(&payment, id).Error; err != nil {
		utils.NotFound(c, "缴费记录不存在")
		return
	}

	utils.Success(c, payment)
}

func CreatePayment(c *gin.Context) {
	var payment models.Payment
	if err := c.ShouldBindJSON(&payment); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	payment.ReceiptNo = generateReceiptNo()
	payment.Status = "paid"

	if payment.Type == "" {
		payment.Type = "tuition"
	}

	tx := database.DB.Begin()

	if err := tx.Create(&payment).Error; err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "创建缴费记录失败")
		return
	}

	if payment.Type == "tuition" && payment.CourseID != nil {
		courseID := *payment.CourseID
		var course models.Course
		if err := tx.First(&course, courseID).Error; err == nil {
			var sc models.StudentCourse
			if err := tx.Where("student_id = ? AND course_id = ?", payment.StudentID, courseID).First(&sc).Error; err != nil {
				tx.Create(&models.StudentCourse{
					StudentID:  payment.StudentID,
					CourseID:   courseID,
					TotalHours: course.TotalHours,
					UsedHours:  0,
				})
			} else if sc.Status == 2 {
				// 已退费的课程重新缴费后恢复在读，课时重新计算
				tx.Model(&sc).Updates(map[string]interface{}{
					"total_hours": course.TotalHours,
					"used_hours":  0,
					"status":      1,
				})
			}
		}
	}

	tx.Commit()
	utils.Success(c, payment)
}

func UpdatePayment(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var payment models.Payment
	if err := database.DB.First(&payment, id).Error; err != nil {
		utils.NotFound(c, "缴费记录不存在")
		return
	}

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	if err := database.DB.Model(&payment).Updates(updates).Error; err != nil {
		utils.InternalServerError(c, "更新失败")
		return
	}

	utils.Success(c, payment)
}

func DeletePayment(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	if err := database.DB.Delete(&models.Payment{}, id).Error; err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	utils.Success(c, nil)
}

// GetRefundQuota 查询学员各在读课程的可退额度
// 可退上限 = 剩余课时 × 课时单价 - 审批中申请已占用的额度
func GetRefundQuota(c *gin.Context) {
	studentID := c.Query("student_id")
	if studentID == "" {
		utils.BadRequest(c, "学员ID不能为空")
		return
	}

	var studentCourses []models.StudentCourse
	if err := database.DB.Where("student_id = ? AND status = ?", studentID, 1).Find(&studentCourses).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	type QuotaItem struct {
		CourseID       uint    `json:"course_id"`
		CourseName     string  `json:"course_name"`
		PricePerHour   float64 `json:"price_per_hour"`
		TotalHours     int     `json:"total_hours"`
		UsedHours      int     `json:"used_hours"`
		RemainingHours int     `json:"remaining_hours"`
		MaxRefund      float64 `json:"max_refund"` // 剩余课时 × 课时单价
		Occupied       float64 `json:"occupied"`   // 审批中申请占用的额度
		Available      float64 `json:"available"`  // 当前可退上限
	}

	quotas := make([]QuotaItem, 0)
	for _, sc := range studentCourses {
		var course models.Course
		if err := database.DB.First(&course, sc.CourseID).Error; err != nil {
			continue
		}

		remaining := sc.TotalHours - sc.UsedHours
		if remaining < 0 {
			remaining = 0
		}
		maxRefund := round2(float64(remaining) * course.PricePerHour)

		var occupied float64
		database.DB.Model(&models.Refund{}).
			Select("COALESCE(SUM(amount), 0)").
			Where("student_id = ? AND course_id = ? AND status = ?", sc.StudentID, sc.CourseID, "pending").
			Scan(&occupied)

		available := round2(maxRefund - occupied)
		if available < 0 {
			available = 0
		}

		quotas = append(quotas, QuotaItem{
			CourseID:       sc.CourseID,
			CourseName:     course.Name,
			PricePerHour:   course.PricePerHour,
			TotalHours:     sc.TotalHours,
			UsedHours:      sc.UsedHours,
			RemainingHours: remaining,
			MaxRefund:      maxRefund,
			Occupied:       round2(occupied),
			Available:      available,
		})
	}

	utils.Success(c, quotas)
}

// CreateRefund 顾问提交退费申请，同一门课审批中的申请会占用可退额度
func CreateRefund(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var req struct {
		StudentID uint    `json:"student_id" binding:"required"`
		CourseID  uint    `json:"course_id" binding:"required"`
		Amount    float64 `json:"amount" binding:"required"`
		Reason    string  `json:"reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	if req.Amount <= 0 {
		utils.BadRequest(c, "退费金额必须大于0")
		return
	}

	tx := database.DB.Begin()

	// 锁定学员课程记录，防止两个顾问同时提交导致合计超额
	var sc models.StudentCourse
	if err := lockForUpdate(tx).
		Where("student_id = ? AND course_id = ?", req.StudentID, req.CourseID).
		First(&sc).Error; err != nil {
		tx.Rollback()
		utils.BadRequest(c, "该学员未报读此课程")
		return
	}

	if sc.Status == 2 {
		tx.Rollback()
		utils.BadRequest(c, "该课程已退费，不能重复申请")
		return
	}

	var course models.Course
	if err := tx.First(&course, sc.CourseID).Error; err != nil {
		tx.Rollback()
		utils.NotFound(c, "课程不存在")
		return
	}

	remaining := sc.TotalHours - sc.UsedHours
	if remaining <= 0 {
		tx.Rollback()
		utils.BadRequest(c, "该课程已无剩余课时，不能申请退费")
		return
	}
	maxRefund := round2(float64(remaining) * course.PricePerHour)

	// 统计同一门课审批中的申请已占用的额度（当前读，保证拿到已提交的最新数据）
	var occupied float64
	lockForUpdate(tx.Model(&models.Refund{})).
		Select("COALESCE(SUM(amount), 0)").
		Where("student_id = ? AND course_id = ? AND status = ?", req.StudentID, req.CourseID, "pending").
		Scan(&occupied)

	available := round2(maxRefund - occupied)
	if available < 0 {
		available = 0
	}

	if round2(req.Amount) > available {
		tx.Rollback()
		utils.BadRequest(c, fmt.Sprintf("退费金额超出可退上限 %.2f 元（剩余课时%d × 单价%.2f元 - 审批中占用%.2f元）", available, remaining, course.PricePerHour, occupied))
		return
	}

	// 关联该课程最近一笔已缴费记录
	var payment models.Payment
	if err := tx.Where("student_id = ? AND course_id = ? AND type = ? AND status = ?",
		req.StudentID, req.CourseID, "tuition", "paid").
		Order("created_at DESC").First(&payment).Error; err != nil {
		tx.Rollback()
		utils.BadRequest(c, "该课程没有已缴费记录，无法申请退费")
		return
	}

	createdBy := userID.(uint)
	refund := models.Refund{
		StudentID: req.StudentID,
		CourseID:  req.CourseID,
		PaymentID: payment.ID,
		Amount:    round2(req.Amount),
		Reason:    req.Reason,
		Status:    "pending",
		CreatedBy: &createdBy,
	}

	if err := tx.Create(&refund).Error; err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "创建退费申请失败")
		return
	}

	tx.Commit()
	utils.Success(c, refund)
}

func GetRefunds(c *gin.Context) {
	status := c.Query("status")

	query := database.DB.Model(&models.Refund{}).
		Preload("Student").Preload("Course").Preload("Payment").Preload("Creator").Preload("Processor")

	if status != "" {
		query = query.Where("status = ?", status)
	}

	var refunds []models.Refund
	if err := query.Order("created_at DESC").Find(&refunds).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, refunds)
}

// ProcessRefund 财务审批退费申请
// 同意：记负数流水、课程剩余课时清零并标为已退费、缴费记录标为已退费
// 驳回：释放该申请占用的可退额度
func ProcessRefund(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID, _ := c.Get("user_id")

	var req struct {
		Status string `json:"status" binding:"required,oneof=approved rejected"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "参数错误，状态必须为 approved 或 rejected")
		return
	}

	tx := database.DB.Begin()

	var refund models.Refund
	if err := lockForUpdate(tx).First(&refund, id).Error; err != nil {
		tx.Rollback()
		utils.NotFound(c, "退费申请不存在")
		return
	}

	if refund.Status != "pending" {
		tx.Rollback()
		utils.BadRequest(c, "该申请已处理，不能重复操作")
		return
	}

	processedBy := userID.(uint)
	refund.ProcessedBy = &processedBy

	if req.Status == "rejected" {
		// 驳回后申请不再占用可退额度
		refund.Status = "rejected"
		if err := tx.Save(&refund).Error; err != nil {
			tx.Rollback()
			utils.InternalServerError(c, "处理退费失败")
			return
		}
		tx.Commit()
		utils.Success(c, refund)
		return
	}

	// 同意退费：锁定学员课程记录，确认课程未退过费且剩余课时仍够退
	var sc models.StudentCourse
	if err := lockForUpdate(tx).
		Where("student_id = ? AND course_id = ?", refund.StudentID, refund.CourseID).
		First(&sc).Error; err != nil {
		tx.Rollback()
		utils.BadRequest(c, "学员课程记录不存在")
		return
	}

	if sc.Status == 2 {
		tx.Rollback()
		utils.BadRequest(c, "该课程已退费，不能重复退费")
		return
	}

	var course models.Course
	if err := tx.First(&course, refund.CourseID).Error; err != nil {
		tx.Rollback()
		utils.NotFound(c, "课程不存在")
		return
	}

	remaining := sc.TotalHours - sc.UsedHours
	if remaining < 0 {
		remaining = 0
	}
	maxRefund := round2(float64(remaining) * course.PricePerHour)
	if refund.Amount > maxRefund {
		tx.Rollback()
		utils.BadRequest(c, fmt.Sprintf("申请后课时又有消耗，当前最多可退 %.2f 元，请驳回后重新申请", maxRefund))
		return
	}

	today := time.Now().Format("2006-01-02")
	refund.Status = "approved"
	refund.RefundDate = &today
	if err := tx.Save(&refund).Error; err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "处理退费失败")
		return
	}

	// 原缴费记录标记为已退费
	paymentMethod := "cash"
	var payment models.Payment
	if err := tx.First(&payment, refund.PaymentID).Error; err == nil {
		paymentMethod = payment.PaymentMethod
		if err := tx.Model(&payment).Update("status", "refunded").Error; err != nil {
			tx.Rollback()
			utils.InternalServerError(c, "更新缴费记录状态失败")
			return
		}
	}

	// 退费记为负数流水，退掉的钱不再计入当月收入
	refundPayment := models.Payment{
		StudentID:     refund.StudentID,
		CourseID:      &refund.CourseID,
		Amount:        -refund.Amount,
		PaymentMethod: paymentMethod,
		PaymentDate:   today,
		Type:          "refund",
		Status:        "paid",
		ReceiptNo:     generateReceiptNo(),
		Remarks:       fmt.Sprintf("退费（%s），退费单#%d", course.Name, refund.ID),
	}
	if err := tx.Create(&refundPayment).Error; err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "创建退费流水失败")
		return
	}

	// 剩余课时清零，课程标记为已退费
	if err := tx.Model(&sc).Updates(map[string]interface{}{
		"used_hours": sc.TotalHours,
		"status":     2,
	}).Error; err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "更新课程状态失败")
		return
	}

	tx.Commit()
	utils.Success(c, refund)
}

func GetFinanceReports(c *gin.Context) {
	reportType := c.DefaultQuery("type", "daily")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	var results []map[string]interface{}
	var groupBy string

	switch reportType {
	case "daily":
		groupBy = "DATE(payment_date)"
	case "monthly":
		groupBy = "SUBSTRING(payment_date, 1, 7)"
	case "yearly":
		groupBy = "SUBSTRING(payment_date, 1, 4)"
	default:
		groupBy = "DATE(payment_date)"
	}

	// 已退费的缴费记录仍计入历史收款，退费部分由负数流水抵扣，合计为净收入
	query := database.DB.Model(&models.Payment{}).
		Select(fmt.Sprintf("%s as period, SUM(amount) as total_income, COUNT(*) as payment_count, payment_method", groupBy)).
		Where("status IN ?", []string{"paid", "refunded"})

	if startDate != "" {
		query = query.Where("payment_date >= ?", startDate)
	}
	if endDate != "" {
		query = query.Where("payment_date <= ?", endDate)
	}

	if err := query.Group(groupBy + ", payment_method").Order("period DESC").Find(&results).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, results)
}

func generateReceiptNo() string {
	return fmt.Sprintf("R%s%06d", time.Now().Format("20060102150405"), time.Now().UnixNano()%1000000)
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
