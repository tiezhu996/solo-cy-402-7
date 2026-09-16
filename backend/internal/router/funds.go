package router

import (
	"cylawcase/internal/middleware"

	"github.com/gin-gonic/gin"
)

// registerFundRoutes 案件资金往来台账路由。
func (r *Router) registerFundRoutes(g *gin.RouterGroup) {
	fund := g.Group("/fund")
	fund.Use(middleware.AuthRequired(r.cfg))
	fund.GET("/entries", r.fund.List)
	fund.GET("/entries/by-case/:id", r.fund.ListByCase)
	fund.GET("/accounts", r.fund.ListAccounts)
	fund.GET("/balance", r.fund.Balance)
	fund.POST("/prepayments", r.fund.Prepayment)
	fund.POST("/expenses", r.fund.Expense)
	fund.POST("/reversals", r.fund.Reverse)
}
