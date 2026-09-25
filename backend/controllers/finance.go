package controllers

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"edu-train/database"
	"edu-train/models"
	"edu-train/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
			studentCourse := models.StudentCourse{
				StudentID:  payment.StudentID,
				CourseID:   courseID,
				TotalHours: course.TotalHours,
				UsedHours:  0,
			}
			tx.Where(models.StudentCourse{StudentID: payment.StudentID, CourseID: courseID}).
				FirstOrCreate(&studentCourse)
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

// refundQuota 某学员某门课当前可退额度信息
type refundQuota struct {
	StudentID       uint    `json:"student_id"`
	CourseID        uint    `json:"course_id"`
	TotalHours      int     `json:"total_hours"`
	UsedHours       int     `json:"used_hours"`
	RemainingHours  int     `json:"remaining_hours"`
	PricePerHour    float64 `json:"price_per_hour"`
	MaxRefundAmount float64 `json:"max_refund_amount"` // 未上课时折算金额
	PendingAmount   float64 `json:"pending_amount"`   // 审批中申请已占用金额
	AvailableAmount float64 `json:"available_amount"` // 当前还能申请的金额
	Refunded        bool    `json:"refunded"`         // 该课程是否已退费
}

// calcRefundQuota 在给定事务内计算可退额度。lock=true 时对学员课程记录加行锁，
// 保证并发提交的两单不会同时读到同一个可退额度。
func calcRefundQuota(tx *gorm.DB, studentID, courseID uint, lock bool) (*refundQuota, error) {
	var sc models.StudentCourse
	scQuery := tx.Where("student_id = ? AND course_id = ?", studentID, courseID)
	if lock {
		scQuery = scQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := scQuery.First(&sc).Error; err != nil {
		return nil, errors.New("该学员未就读此课程")
	}

	var course models.Course
	if err := tx.First(&course, courseID).Error; err != nil {
		return nil, errors.New("课程不存在")
	}

	var pendingAmount float64
	tx.Model(&models.Refund{}).
		Where("student_id = ? AND course_id = ? AND status = ?", studentID, courseID, "pending").
		Select("COALESCE(SUM(amount), 0)").Scan(&pendingAmount)

	remaining := sc.TotalHours - sc.UsedHours
	if remaining < 0 {
		remaining = 0
	}
	maxAmount := roundMoney(float64(remaining) * course.PricePerHour)
	available := maxAmount - pendingAmount
	if available < 0 {
		available = 0
	}

	return &refundQuota{
		StudentID:       studentID,
		CourseID:        courseID,
		TotalHours:      sc.TotalHours,
		UsedHours:       sc.UsedHours,
		RemainingHours:  remaining,
		PricePerHour:    course.PricePerHour,
		MaxRefundAmount: maxAmount,
		PendingAmount:   roundMoney(pendingAmount),
		AvailableAmount: roundMoney(available),
		Refunded:        sc.Status == 2,
	}, nil
}

func roundMoney(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// GetRefundQuota GET /refunds/quota?student_id=&course_id=
// 顾问打开申请框时查询：系统按未上课时×单价给出最多可退金额，
// 并扣除同一门课仍在审批中的申请占用额度。
func GetRefundQuota(c *gin.Context) {
	studentID, err := strconv.Atoi(c.Query("student_id"))
	if err != nil || studentID <= 0 {
		utils.BadRequest(c, "请选择学员")
		return
	}
	courseID, err := strconv.Atoi(c.Query("course_id"))
	if err != nil || courseID <= 0 {
		utils.BadRequest(c, "请选择课程")
		return
	}

	var quota *refundQuota
	var qErr error
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		quota, qErr = calcRefundQuota(tx, uint(studentID), uint(courseID), false)
		return nil
	}); err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}
	if qErr != nil {
		utils.BadRequest(c, qErr.Error())
		return
	}

	utils.Success(c, quota)
}

// CreateRefund 顾问提交退费申请。申请进入审批中即占用该课程的可退额度。
func CreateRefund(c *gin.Context) {
	var req struct {
		StudentID uint    `json:"student_id"`
		CourseID  uint    `json:"course_id"`
		PaymentID uint    `json:"payment_id"`
		Amount    float64 `json:"amount"`
		Reason    string  `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}
	if req.StudentID == 0 || req.CourseID == 0 {
		utils.BadRequest(c, "请选择学员和课程")
		return
	}
	if req.Amount <= 0 {
		utils.BadRequest(c, "退费金额必须大于0")
		return
	}

	userID, _ := c.Get("user_id")
	applicantID := userID.(uint)
	req.Amount = roundMoney(req.Amount)

	var refund models.Refund
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		quota, err := calcRefundQuota(tx, req.StudentID, req.CourseID, true)
		if err != nil {
			return err
		}
		if quota.Refunded {
			return errors.New("该课程已退费，不能重复申请")
		}
		if req.Amount > quota.AvailableAmount {
			return fmt.Errorf("退费金额超出可退额度，当前最多可退 %.2f 元", quota.AvailableAmount)
		}

		paymentID := req.PaymentID
		if paymentID == 0 {
			// 未指定缴费单时，自动匹配该学员该课程一笔未退费的学费缴费记录
			var payment models.Payment
			if err := tx.Where("student_id = ? AND course_id = ? AND type = ? AND status = ?",
				req.StudentID, req.CourseID, "tuition", "paid").
				Order("payment_date DESC, id DESC").First(&payment).Error; err != nil {
				return errors.New("找不到该课程可退费的缴费记录")
			}
			paymentID = payment.ID
		} else {
			var payment models.Payment
			if err := tx.First(&payment, paymentID).Error; err != nil {
				return errors.New("缴费记录不存在")
			}
			if payment.StudentID != req.StudentID ||
				payment.CourseID == nil || *payment.CourseID != req.CourseID {
				return errors.New("缴费记录与所选学员/课程不一致")
			}
			if payment.Type != "tuition" || payment.Status != "paid" {
				return errors.New("该缴费记录已退费或不可退")
			}
		}

		refund = models.Refund{
			StudentID:    req.StudentID,
			CourseID:     req.CourseID,
			PaymentID:    paymentID,
			Amount:       req.Amount,
			Reason:       req.Reason,
			Status:       "pending",
			AppliedBy:    &applicantID,
			TotalHours:   quota.TotalHours,
			UsedHours:    quota.UsedHours,
			PricePerHour: quota.PricePerHour,
		}
		return tx.Create(&refund).Error
	})

	if err != nil {
		utils.BadRequest(c, err.Error())
		return
	}

	utils.Success(c, refund)
}

func GetRefunds(c *gin.Context) {
	status := c.Query("status")
	studentID := c.Query("student_id")

	query := database.DB.Model(&models.Refund{}).
		Preload("Student").Preload("Course").
		Preload("AppliedUser").Preload("ProcessedUser")

	if status != "" {
		query = query.Where("status = ?", status)
	}
	if studentID != "" {
		query = query.Where("student_id = ?", studentID)
	}

	var refunds []models.Refund
	if err := query.Order("created_at DESC").Find(&refunds).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, refunds)
}

// ProcessRefund 财务审批：同意后生成负数退费流水、清零该课程剩余课时并标记已退费、
// 原缴费记录标记已退费，退掉的钱不再计入收入（收入只统计 status=paid）。
// 驳回则只改状态，审批中占用的额度随即释放。
func ProcessRefund(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID, _ := c.Get("user_id")
	processedBy := userID.(uint)

	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}
	if req.Status != "approved" && req.Status != "rejected" {
		utils.BadRequest(c, "审批状态只能是 approved 或 rejected")
		return
	}

	var refund models.Refund
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		// 锁定退费单本身，避免两个财务同时处理
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&refund, id).Error; err != nil {
			return errors.New("退费申请不存在")
		}
		if refund.Status != "pending" {
			return errors.New("该申请已处理，不能重复审批")
		}

		if req.Status == "rejected" {
			refund.Status = "rejected"
			refund.ProcessedBy = &processedBy
			return tx.Save(&refund).Error
		}

		// 同意前重新核对额度（审批期间课时可能又有消耗）
		quota, err := calcRefundQuota(tx, refund.StudentID, refund.CourseID, true)
		if err != nil {
			return err
		}
		if quota.Refunded {
			return errors.New("该课程已退费，申请不能再同意")
		}
		// AvailableAmount 已扣除包含本单在内的全部审批中金额，
		// 这里只需确认除本单外的占用 + 本单金额没有超过折算总额
		otherPending := quota.PendingAmount - refund.Amount
		if refund.Amount > quota.MaxRefundAmount-otherPending {
			return errors.New("可退额度不足，不能同意该申请")
		}

		var payment models.Payment
		if err := tx.First(&payment, refund.PaymentID).Error; err != nil {
			return errors.New("缴费记录不存在")
		}
		if payment.Status != "paid" {
			return errors.New("缴费记录已退费，申请不能再同意")
		}

		today := time.Now().Format("2006-01-02")

		// 1. 原缴费记录标记为已退费
		if err := tx.Model(&payment).Updates(map[string]interface{}{
			"status": "refunded",
		}).Error; err != nil {
			return err
		}

		// 2. 退费记成负数流水（不计入收入：收入只统计 status=paid 的记录）
		refundFlow := models.Payment{
			StudentID:     refund.StudentID,
			CourseID:      &refund.CourseID,
			Amount:        -refund.Amount,
			PaymentMethod: payment.PaymentMethod,
			PaymentDate:   today,
			Type:          "refund",
			Status:        "refunded",
			ReceiptNo:     generateRefundReceiptNo(),
			Remarks:       fmt.Sprintf("退费流水（退费申请#%d，原收据号 %s）", refund.ID, payment.ReceiptNo),
			RefundID:      &refund.ID,
		}
		if err := tx.Create(&refundFlow).Error; err != nil {
			return err
		}

		// 3. 这门课剩余课时清零并标记为已退费
		if err := tx.Model(&models.StudentCourse{}).
			Where("student_id = ? AND course_id = ?", refund.StudentID, refund.CourseID).
			Updates(map[string]interface{}{
				"used_hours": gorm.Expr("total_hours"),
				"status":     2,
				"end_date":   today,
			}).Error; err != nil {
			return err
		}

		// 4. 同一门课其他还在审批中的申请无法再退（课程已退费），自动驳回释放占用
		if err := tx.Model(&models.Refund{}).
			Where("student_id = ? AND course_id = ? AND status = ? AND id <> ?",
				refund.StudentID, refund.CourseID, "pending", refund.ID).
			Updates(map[string]interface{}{
				"status":       "rejected",
				"processed_by": processedBy,
				"reason":       gorm.Expr("CONCAT(IFNULL(reason, ''), ?)", " ｜ 该课程已通过其他退费申请，本单自动驳回"),
			}).Error; err != nil {
			return err
		}

		refund.Status = "approved"
		refund.RefundDate = &today
		refund.ProcessedBy = &processedBy
		return tx.Save(&refund).Error
	})

	if err != nil {
		utils.BadRequest(c, err.Error())
		return
	}

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

	query := database.DB.Model(&models.Payment{}).
		Select(fmt.Sprintf("%s as period, SUM(amount) as total_income, COUNT(*) as payment_count, payment_method", groupBy)).
		Where("status = ?", "paid")

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

func generateRefundReceiptNo() string {
	return fmt.Sprintf("RF%s%06d", time.Now().Format("20060102150405"), time.Now().UnixNano()%1000000)
}
