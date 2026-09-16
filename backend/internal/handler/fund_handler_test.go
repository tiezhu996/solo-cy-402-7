package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"cylawcase/internal/database"
	"cylawcase/internal/model"
	"cylawcase/internal/repository"
	"cylawcase/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newFundRouter(t *testing.T) (*gin.Engine, *gorm.DB, uint64, uint64) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := filepath.Join(t.TempDir(), "h.db") + "?_pragma=busy_timeout(10000)&_time_format=sqlite"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Client{}, &model.Case{},
		&model.FundAccount{}, &model.FundEntry{}); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureFundIndexes(db); err != nil {
		t.Fatal(err)
	}
	client := &model.Client{Name: "客户甲"}
	db.Create(client)
	cs := &model.Case{CaseNo: "C-1", Title: "案件一", ClientID: client.ID, LeadLawyerID: 2, CoLawyerIDs: model.CoLawyerJSON("[]")}
	db.Create(cs)
	other := &model.Client{Name: "客户乙"}
	db.Create(other)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.NewFundService(repository.NewFundRepository(db),
		repository.NewCaseRepository(db), repository.NewClientRepository(db), logger)
	h := NewFundHandler(svc, logger)

	r := gin.New()
	auth := func(c *gin.Context) { c.Set("user_id", uint64(2)); c.Set("username", "lawyer"); c.Next() }
	g := r.Group("/api/v1/fund", auth)
	g.GET("/entries", h.List)
	g.GET("/balance", h.Balance)
	g.POST("/prepayments", h.Prepayment)
	g.POST("/expenses", h.Expense)
	g.POST("/reversals", h.Reverse)
	return r, db, cs.ID, other.ID
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	raw, _ := io.ReadAll(w.Body)
	_ = json.Unmarshal(raw, &out)
	return w.Code, out
}

func entryID(data any) uint64 {
	m := data.(map[string]any)["entry"].(map[string]any)
	return uint64(m["id"].(float64))
}

// 端到端：预收幂等、支出余额不足整笔拒绝、冲销仅一次、归属校验、余额一致。
func TestFundHTTPEndToEnd(t *testing.T) {
	r, _, caseID, otherClient := newFundRouter(t)
	const idem1 = "uuid-pre-1"

	// 首次预收 100.00
	code, body := doJSON(t, r, "POST", "/api/v1/fund/prepayments", map[string]any{
		"case_id": caseID, "client_id": 1, "amount": "100.00",
		"subject": "预收", "idempotency_key": idem1,
	})
	if code != http.StatusOK || body["code"].(float64) != 0 {
		t.Fatalf("prepay code=%d body=%v", code, body)
	}
	if body["data"].(map[string]any)["replayed"].(bool) {
		t.Fatal("first prepay must not be replayed")
	}

	// 同键重试：幂等返回同一笔，不重复入账。
	code, body2 := doJSON(t, r, "POST", "/api/v1/fund/prepayments", map[string]any{
		"case_id": caseID, "client_id": 1, "amount": "100.00",
		"subject": "别的", "idempotency_key": idem1,
	})
	if code != 200 || !body2["data"].(map[string]any)["replayed"].(bool) {
		t.Fatalf("retry must replay: code=%d body=%v", code, body2)
	}

	// 超额支出 150 → 409 / 40903，整笔拒绝。
	code, body = doJSON(t, r, "POST", "/api/v1/fund/expenses", map[string]any{
		"case_id": caseID, "client_id": 1, "amount": "150.00",
		"subject": "超额", "idempotency_key": "exp-big",
	})
	if code != http.StatusConflict || body["code"].(float64) != 40903 {
		t.Fatalf("overdraw code=%d body=%v", code, body)
	}

	// 合规支出 60 → 成功，余额 40。
	code, body = doJSON(t, r, "POST", "/api/v1/fund/expenses", map[string]any{
		"case_id": caseID, "client_id": 1, "amount": "60.00",
		"subject": "差旅", "idempotency_key": "exp-60",
	})
	if code != 200 {
		t.Fatalf("expense: %v", body)
	}
	expenseID := entryID(body["data"])

	// 余额查询：物化与重算一致为 40。
	code, body = doJSON(t, r, "GET", "/api/v1/fund/balance?case_id="+itoaH(int(caseID))+"&client_id=1", nil)
	if code != 200 || body["data"].(map[string]any)["balance"] != "40.00" ||
		!body["data"].(map[string]any)["consistent"].(bool) {
		t.Fatalf("balance: %v", body["data"])
	}

	// 冲销该支出 → 余额恢复 100。
	code, body = doJSON(t, r, "POST", "/api/v1/fund/reversals", map[string]any{
		"entry_id": expenseID, "reason": "录错", "idempotency_key": "rev-1",
	})
	if code != 200 {
		t.Fatalf("reverse: %v", body)
	}

	// 同一明细再次冲销 → 409 / 40904，拒绝重复冲销。
	code, body = doJSON(t, r, "POST", "/api/v1/fund/reversals", map[string]any{
		"entry_id": expenseID, "reason": "再冲", "idempotency_key": "rev-2",
	})
	if code != http.StatusConflict || body["code"].(float64) != 40904 {
		t.Fatalf("double reverse code=%d body=%v", code, body)
	}

	// 列表读回：原支出明细仍显示已冲销（reversed=true、reversed_by_id 指向冲销明细），
	// 且包含那条只追加的冲销明细；原明细金额等字段不变。
	code, body = doJSON(t, r, "GET", "/api/v1/fund/entries?case_id="+itoaH(int(caseID))+"&page_size=50", nil)
	if code != 200 {
		t.Fatalf("list entries: %v", body)
	}
	list := body["data"].(map[string]any)["list"].([]any)
	var originOut, reversalOut map[string]any
	for _, it := range list {
		row := it.(map[string]any)
		if uint64(row["id"].(float64)) == expenseID {
			originOut = row
		}
		if row["entry_type"] == "reversal" {
			reversalOut = row
		}
	}
	if originOut == nil || reversalOut == nil {
		t.Fatalf("expected origin expense and reversal in list, got %v", list)
	}
	if originOut["reversed"] != true {
		t.Fatalf("origin expense must read as reversed: %v", originOut)
	}
	if uint64(originOut["reversed_by_id"].(float64)) != uint64(reversalOut["id"].(float64)) {
		t.Fatalf("reversed_by_id=%v must point to reversal id=%v", originOut["reversed_by_id"], reversalOut["id"])
	}
	if originOut["delta"] != "-60.00" || originOut["balance"] != "40.00" {
		t.Fatalf("origin expense fields changed after reversal: %v", originOut)
	}

	// 归属不一致（案件属于客户1，却挂客户乙）→ 422 / 42200。
	code, body = doJSON(t, r, "POST", "/api/v1/fund/prepayments", map[string]any{
		"case_id": caseID, "client_id": otherClient, "amount": "10.00",
		"idempotency_key": "mis",
	})
	if code != http.StatusUnprocessableEntity || body["code"].(float64) != 42200 {
		t.Fatalf("mismatch code=%d body=%v", code, body)
	}

	// 金额超过两位小数 → 400。
	code, _ = doJSON(t, r, "POST", "/api/v1/fund/prepayments", map[string]any{
		"case_id": caseID, "client_id": 1, "amount": "10.123",
		"idempotency_key": "badamt",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("bad amount code=%d, want 400", code)
	}
}

func itoaH(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
