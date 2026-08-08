package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useRebateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &Log{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.TopupRebateInviterPercent = 0.10
		common.TopupRebateInviteePercent = 0.05
		common.TopupRebateTarget = "aff_quota"
		common.TopupRebateLimit = 0
		common.TopupRebateStartTime = 0
		common.QuotaPerUnit = 500 * 1000.0
		_ = sqlDB.Close()
	})
	return db
}

func createRebateUser(t *testing.T, db *gorm.DB, username string, inviterID int) int {
	t.Helper()
	u := User{
		Username:  username,
		AffCode:   common.GetRandomString(4),
		InviterId: inviterID,
		Role:      common.RoleCommonUser,
		Status:    common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&u).Error)
	return u.Id
}

func setRebateOptions(t *testing.T, inviter, invitee float64, target string) {
	t.Helper()
	common.TopupRebateInviterPercent = inviter
	common.TopupRebateInviteePercent = invitee
	common.TopupRebateTarget = target
}

func TestCreditRebateNoInviterNoCredit(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	uid := createRebateUser(t, db, "no-inviter", 0)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := CreditRebate(tx, uid, 1_000_000, "充值", "tn-no-inviter")
		return err
	}))

	var u User
	require.NoError(t, db.First(&u, uid).Error)
	assert.Zero(t, u.AffQuota)
	assert.Zero(t, u.AffHistoryQuota)
	assert.Zero(t, u.Quota)
}

func TestCreditRebateZeroAmountNoCredit(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	inviterID := createRebateUser(t, db, "zero-inviter", 0)
	inviteeID := createRebateUser(t, db, "zero-invitee", inviterID)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := CreditRebate(tx, inviteeID, 0, "充值", "tn-zero")
		return err
	}))

	var inviter, invitee User
	require.NoError(t, db.First(&inviter, inviterID).Error)
	require.NoError(t, db.First(&invitee, inviteeID).Error)
	assert.Zero(t, inviter.AffQuota)
	assert.Zero(t, invitee.AffQuota)
}

func TestCreditRebateSelfInviteNoCredit(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	uid := createRebateUser(t, db, "self-invite", 0)
	require.NoError(t, db.Model(&User{}).Where("id = ?", uid).Update("inviter_id", uid).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := CreditRebate(tx, uid, 1_000_000, "充值", "tn-self")
		return err
	}))

	var u User
	require.NoError(t, db.First(&u, uid).Error)
	assert.Zero(t, u.AffQuota)
	assert.Zero(t, u.Quota)
}

func TestCreditRebateAffQuotaTarget(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	inviterID := createRebateUser(t, db, "inviter", 0)
	inviteeID := createRebateUser(t, db, "invitee", inviterID)

	const source = int64(1_000_000)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := CreditRebate(tx, inviteeID, source, "充值", "tn-aff")
		return err
	}))

	var inviter, invitee User
	require.NoError(t, db.First(&inviter, inviterID).Error)
	require.NoError(t, db.First(&invitee, inviteeID).Error)
	assert.Equal(t, int64(100_000), int64(inviter.AffQuota))
	assert.Equal(t, int64(100_000), int64(inviter.AffHistoryQuota))
	assert.Equal(t, int64(50_000), int64(invitee.AffQuota))
	assert.Equal(t, int64(50_000), int64(invitee.AffHistoryQuota))
	assert.Zero(t, invitee.Quota)
}

func TestCreditRebateQuotaTarget(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "quota")
	inviterID := createRebateUser(t, db, "qt-inviter", 0)
	inviteeID := createRebateUser(t, db, "qt-invitee", inviterID)

	const source = int64(1_000_000)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := CreditRebate(tx, inviteeID, source, "充值", "tn-quota")
		return err
	}))

	var inviter, invitee User
	require.NoError(t, db.First(&inviter, inviterID).Error)
	require.NoError(t, db.First(&invitee, inviteeID).Error)
	assert.Equal(t, int64(100_000), int64(inviter.Quota))
	assert.Equal(t, int64(50_000), int64(invitee.Quota))
	assert.Zero(t, inviter.AffQuota)
	assert.Zero(t, invitee.AffQuota)
}

func TestRechargeWaffoRebateOnlyOnce(t *testing.T) {
	db := useRebateTestDB(t)
	common.QuotaPerUnit = 500 * 1000.0
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	inviterID := createRebateUser(t, db, "waffo-inviter", 0)
	inviteeID := createRebateUser(t, db, "waffo-invitee", inviterID)

	topUp := TopUp{
		UserId:          inviteeID,
		Amount:          2,
		Money:           0.004,
		TradeNo:         "waffo-tn-1",
		PaymentMethod:   PaymentMethodWaffo,
		PaymentProvider: PaymentProviderWaffo,
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, db.Create(&topUp).Error)

	// 首次回调 + 重复回调：只应到账一次。
	require.NoError(t, RechargeWaffo("waffo-tn-1", "127.0.0.1"))
	require.NoError(t, RechargeWaffo("waffo-tn-1", "127.0.0.1"))

	var invitee User
	require.NoError(t, db.First(&invitee, inviteeID).Error)
	assert.Equal(t, int64(1_000_000), int64(invitee.Quota)) // 2 * 500000，只加一次
	var inviter User
	require.NoError(t, db.First(&inviter, inviterID).Error)
	assert.Equal(t, int64(100_000), int64(inviter.AffQuota))
	assert.Equal(t, int64(100_000), int64(inviter.AffHistoryQuota))
	assert.Equal(t, int64(50_000), int64(invitee.AffQuota))
	assert.Equal(t, int64(50_000), int64(invitee.AffHistoryQuota))
}

func TestCreditRebateSurfacesDBError(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	inviterID := createRebateUser(t, db, "db-err-inviter", 0)
	inviteeID := createRebateUser(t, db, "db-err-invitee", inviterID)

	// 关闭底层连接，使后续任何 DB 操作都失败：
	// 邀请人查询错误必须向上传播（而非被当作"无邀请人"静默吞掉），
	// 这样调用方才能回滚事务，避免"给了额度却丢了返现"。
	raw, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	tx := db.Begin()
	_, err = CreditRebate(tx, inviteeID, 1_000_000, "test", "tn-db-err")
	require.Error(t, err)
}

func TestCreditRebateLimitStopsAfterNTimes(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	common.TopupRebateLimit = 1
	inviterID := createRebateUser(t, db, "limit-inviter", 0)
	inviteeID := createRebateUser(t, db, "limit-invitee", inviterID)

	rebate := func(tradeNo string) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := CreditRebate(tx, inviteeID, 1_000_000, "充值", tradeNo)
			return err
		}))
	}

	// 模拟订单已入账（成功状态），CreditRebate 按成功订单笔数计数。
	succeed := func(tradeNo string) {
		t.Helper()
		require.NoError(t, db.Create(&TopUp{
			UserId:      inviteeID,
			Amount:      1,
			Money:       0.002,
			TradeNo:     tradeNo,
			CreateTime:  common.GetTimestamp(),
			Status:      common.TopUpStatusSuccess,
		}).Error)
	}

	// 第 1 笔：订单已成功，应返利。
	succeed("limit-tn-1")
	rebate("limit-tn-1")

	// 第 2 笔：此时成功笔数=2 > 上限 1，应停止返利。
	succeed("limit-tn-2")
	rebate("limit-tn-2")

	var inviter, invitee User
	require.NoError(t, db.First(&inviter, inviterID).Error)
	require.NoError(t, db.First(&invitee, inviteeID).Error)
	assert.Equal(t, int64(100_000), int64(inviter.AffQuota), "仅第 1 笔返利给邀请人")
	assert.Equal(t, int64(50_000), int64(invitee.AffQuota), "仅第 1 笔返利给被邀请人")
}

func TestCreditRebateStartTimeExcludesOldOrders(t *testing.T) {
	db := useRebateTestDB(t)
	setRebateOptions(t, 0.10, 0.05, "aff_quota")
	common.TopupRebateLimit = 1
	startTime := common.GetTimestamp()
	common.TopupRebateStartTime = startTime
	inviterID := createRebateUser(t, db, "st-inviter", 0)
	inviteeID := createRebateUser(t, db, "st-invitee", inviterID)

	rebate := func(tradeNo string) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := CreditRebate(tx, inviteeID, 1_000_000, "充值", tradeNo)
			return err
		}))
	}
	succeed := func(tradeNo string, createTime int64) {
		t.Helper()
		require.NoError(t, db.Create(&TopUp{
			UserId:      inviteeID,
			Amount:      1,
			Money:       0.002,
			TradeNo:     tradeNo,
			CreateTime:  createTime,
			Status:      common.TopUpStatusSuccess,
		}).Error)
	}

	// 上线前的旧订单不参与计数。
	succeed("st-old", startTime-100)
	rebate("st-old")

	// 上线后的第 1 笔订单：计入第 1 笔，返利。
	succeed("st-new-1", startTime)
	rebate("st-new-1")

	// 上线后的第 2 笔订单：超过上限 1，不返。
	succeed("st-new-2", startTime+1)
	rebate("st-new-2")

	var inviter, invitee User
	require.NoError(t, db.First(&inviter, inviterID).Error)
	require.NoError(t, db.First(&invitee, inviteeID).Error)
	assert.Equal(t, int64(100_000), int64(inviter.AffQuota), "旧订单不计数，新订单仍给满 1 笔返利")
	assert.Equal(t, int64(50_000), int64(invitee.AffQuota))
}