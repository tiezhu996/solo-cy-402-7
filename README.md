# LexCase（律师案件管理系统）

面向律师事务所的一体化管理平台，支持客户管理、案件全流程跟踪、文档归档和费用结算。

## 快速启动（Docker Compose 一键部署）

```bash
cp .env.example .env
docker compose up -d --build
```

启动完成后访问：

- 前端：http://localhost:28031
- 后端 API：http://localhost:29069
- 后端健康检查：http://localhost:29069/healthz
- PostgreSQL：localhost:38102

预置账号（密码见 database/init.sql 与 backend/internal/service/seed.go）：

| 用户名 | 密码 | 角色 |
| --- | --- | --- |
| admin | Admin@123 | 管理员 |
| lawyer | User@123 | 律师 |
| assistant | User@123 | 助理 |

## 本地开发

后端：

```bash
cd backend && go mod tidy && go run ./cmd/server
```

构建：`cd backend && go build ./...`

前端：

```bash
cd frontend && npm install && npm run dev
```

前端开发服务器通过 Vite 代理将 `/api` 转发到 `http://localhost:29069`。

## 技术栈

| 层 | 技术 |
| --- | --- |
| 前端 | React 18 + TypeScript + Ant Design + Vite + Zustand |
| 后端 | Go 1.22 + Gin + GORM |
| 数据库 | PostgreSQL 16 |
| 认证 | JWT（github.com/golang-jwt/jwt/v5）+ RBAC |
| 其他依赖 | gin-contrib/cors、golang.org/x/crypto/bcrypt、go-playground/validator/v10 |

## 项目目录结构

```
cy-402/
├── docker-compose.yml
├── .env.example
├── database/init.sql              # PostgreSQL 初始化脚本（建表 + 种子数据）
├── backend/
│   ├── cmd/server/main.go
│   └── internal/
│       ├── config/
│       ├── model/                 # user/client/case/document/billing/audit_log
│       ├── repository/            # 按实体分文件
│       ├── service/               # 业务逻辑 + 种子数据 + 单元测试
│       ├── handler/               # HTTP 处理器（含 upload_handler、audit_log_handler）
│       ├── router/                # router.go + 按实体分文件
│       ├── middleware/            # auth/rbac/rate_limiter/error_handler/audit_log/cors/request_logger
│       ├── dto/
│       ├── constants/             # 枚举、错误码、日志模板、文案
│       └── util/                  # jwt/logger/formatters/amount_formatter/app_error/file_upload
└── frontend/
    └── src/
        ├── api/                   # auth/user/client/case/document/billing/auditLog/upload
        ├── stores/                # authStore/userStore/clientStore/caseStore/documentStore/billingStore
        ├── types/
        ├── components/common/     # CaseCard/DocumentList/StatusBadge/TimelineItem/AmountSummary/ClientCard/CaseTable/BillingCard/DocumentCard/FileUploader/FilterBar/AvatarUploader/PermissionGuard
        ├── hooks/                 # useAuth/usePagination/useFileUpload/usePermission
        ├── pages/                 # Cases/CaseDetail/Clients/Billing/Documents/Profile/AuditLogs/Login
        ├── router/                # index.tsx + guards.tsx
        ├── utils/                 # dateFormat/amountFormatter/request
        └── constants/             # case/billing/document/errorCodes
```

## 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| COMPOSE_PROJECT_NAME | Docker Compose 项目名/容器前缀 | cylawcase |
| DB_NAME | 数据库名 | cylawcase_db |
| DB_USER | 数据库用户 | cylawcase_user |
| DB_PASSWORD | 数据库密码 | cylawcase_pwd |
| DB_ROOT_PASSWORD | 预留 root 密码项 | cylawcase_root |
| JWT_SECRET | JWT 签名密钥 | change_me_to_a_long_random_string |
| JWT_EXPIRE_HOURS | JWT 过期小时数 | 72 |
| APP_CORS_ORIGINS | 允许的跨域来源（逗号分隔） | http://localhost:28031 |
| FRONTEND_PORT | 前端端口 | 28031 |
| BACKEND_PORT | 后端端口 | 29069 |
| DB_PORT | 数据库端口 | 38102 |

## Docker 部署说明

- 端口映射：前端 28031:80、后端 29069:8080、PostgreSQL 38102:5432。
- 数据卷：`db-data` 持久化 PostgreSQL 数据；`upload-data` 持久化上传文件。
- 服务依赖：backend `depends_on` db（service_healthy），frontend `depends_on` backend（service_healthy）。
- 前端 Nginx 将 `/api/` 反代到 `http://backend:8080/`，支持 SPA 路由 `try_files`。
- 常见问题：
  - 端口冲突：修改 `.env` 中 `FRONTEND_PORT/BACKEND_PORT/DB_PORT` 后重新 `docker compose up -d`。
  - 数据库重置：`docker compose down -v` 后重新启动。

## 枚举出现位置清单

### CaseStatus（filed/investigating/hearing/closed/archived）
- 后端：`backend/internal/constants/case.go`、`backend/internal/model/case.go`、`backend/internal/service/case_service.go`、`backend/internal/util/formatters.go`、`backend/internal/constants/log_templates.go`、`backend/internal/constants/error_codes.go`、`backend/internal/dto/dto_case.go`、`database/init.sql`
- 前端：`frontend/src/constants/case.ts`、`frontend/src/components/common/StatusBadge.tsx`、`frontend/src/pages/Cases.tsx`、`frontend/src/pages/CaseDetail.tsx`、`frontend/src/components/common/CaseCard.tsx`、`frontend/src/components/common/CaseTable.tsx`

### BillingType（attorney_fee/court_fee/travel_fee/other）
- 后端：`backend/internal/constants/billing.go`、`backend/internal/model/billing.go`、`backend/internal/service/billing_service.go`、`backend/internal/util/formatters.go`、`backend/internal/constants/log_templates.go`、`backend/internal/constants/error_codes.go`、`backend/internal/dto/dto_billing.go`、`database/init.sql`
- 前端：`frontend/src/constants/billing.ts`、`frontend/src/components/common/BillingCard.tsx`、`frontend/src/pages/Billing.tsx`

### BillingStatus（pending/paid/invoiced/void）
- 后端：`backend/internal/constants/billing.go`、`backend/internal/model/billing.go`、`backend/internal/service/billing_service.go`、`backend/internal/util/formatters.go`、`backend/internal/constants/log_templates.go`、`backend/internal/constants/error_codes.go`、`database/init.sql`
- 前端：`frontend/src/constants/billing.ts`、`frontend/src/components/common/StatusBadge.tsx`、`frontend/src/components/common/AmountSummary.tsx`、`frontend/src/components/common/BillingCard.tsx`、`frontend/src/pages/Billing.tsx`

## API 接口清单

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | /healthz | 服务健康检查 |
| GET | /api/healthz | Nginx 反代健康检查 |
| GET | /api/v1/healthz | API 版本健康检查 |
| POST | /api/v1/auth/register | 用户注册 |
| POST | /api/v1/auth/login | 用户登录，返回 JWT |
| GET | /api/v1/users/me | 当前登录用户信息 |
| PUT | /api/v1/users/me | 修改个人资料 |
| GET | /api/v1/users | 用户列表（仅管理员） |
| GET | /api/v1/clients | 客户分页列表 |
| POST | /api/v1/clients | 新建客户 |
| GET | /api/v1/clients/:id | 客户详情与历史案件 |
| PUT | /api/v1/clients/:id | 编辑客户 |
| DELETE | /api/v1/clients/:id | 删除客户 |
| GET | /api/v1/cases | 案件分页列表 |
| POST | /api/v1/cases | 创建案件 |
| GET | /api/v1/cases/:id | 案件详情 |
| PUT | /api/v1/cases/:id | 更新案件 |
| POST | /api/v1/cases/:id/status | 案件状态流转 |
| POST | /api/v1/cases/:id/assign | 分配主办律师 |
| GET | /api/v1/documents | 文档中心分页列表 |
| POST | /api/v1/documents | 上传文档记录 |
| GET | /api/v1/documents/by-case/:id | 按案件查询文档 |
| DELETE | /api/v1/documents/:id | 删除文档 |
| GET | /api/v1/billings | 账单分页列表 |
| POST | /api/v1/billings | 创建账单 |
| GET | /api/v1/billings/summary | 本月应收/已收/待收汇总 |
| GET | /api/v1/billings/by-case/:id | 按案件查询账单 |
| POST | /api/v1/billings/:id/paid | 标记支付 |
| POST | /api/v1/billings/:id/invoiced | 标记开票 |
| POST | /api/v1/billings/:id/void | 作废账单 |
| GET | /api/v1/fund/entries | 资金往来明细分页（可按案件/客户/类型过滤） |
| GET | /api/v1/fund/entries/by-case/:id | 按案件查询全部资金明细（时间正序） |
| GET | /api/v1/fund/accounts | 专案账户余额分页（附按明细重算余额与一致性标记） |
| GET | /api/v1/fund/balance?case_id=&client_id= | 查询某案件+客户专案余额（物化值与重算值） |
| POST | /api/v1/fund/prepayments | 登记客户预收款（需 idempotency_key） |
| POST | /api/v1/fund/expenses | 登记办案支出（需 idempotency_key，余额不足整笔拒绝） |
| POST | /api/v1/fund/reversals | 反向冲销某条已入账明细（需 entry_id/reason/idempotency_key） |
| GET | /api/v1/audit-logs | 审计日志（仅管理员） |
| POST | /api/v1/upload/file | 文件上传 |

## 主要功能

- 客户管理：新建/编辑/检索客户，查看历史案件。
- 案件管理：创建案件、状态流转（立案→调查→庭审→结案→归档）、律师分配、筛选查询。
- 文档归档：按案件上传/查看/删除文档（起诉状/答辩状/证据/判决书/合同等）。
- 费用结算：创建账单、标记支付、开票、作废，本月应收/已收/待收汇总。
- 案件资金往来台账：登记客户预收款与办案支出，专案余额管控，录错只能反向冲销。
- 审计日志：写操作自动记录（管理员查看）。
- 角色权限：JWT + RBAC（admin/lawyer/assistant）。

## 案件资金往来台账（资金安全设计）

每个「案件 + 客户」对应唯一专案资金账户，律师登记的每一笔预收、支出都必须归属同一案件及其本人客户。
金额在后端统一以「分」(int64) 存储与计算，对外 JSON 使用两位小数字符串，杜绝浮点误差。

业务不变量：

- **同案同客户**：写入时校验案件存在、客户存在，且 `case.client_id` 与提交客户一致，否则拒绝（422）。
- **专案余额、整笔拒绝**：支出只能使用该案可用余额。原子条件更新
  `UPDATE fund_accounts SET balance_cents = balance_cents + ? WHERE id=? AND balance_cents + ? >= 0`
  配合 `CHECK (balance_cents >= 0)`，余额不足时整笔事务回滚——**不写明细、不动余额，原明细保持不变**（409 / 40903）。
- **只追加、不可改删**：明细为 append-only，不存在 update/delete 接口；录错只能新增一笔方向相反的冲销明细。
  冲销**绝不回写原行**——原明细的金额、入账后余额快照、幂等键及所有列保持入账时原样；
  冲销关联只存在于新行的 `reversal_of_id` 上，「已冲销 / 被哪笔冲销」在读取时按该列反向关联临时派生。
- **冲销只能一次**：完全由部分唯一索引
  `CREATE UNIQUE INDEX ... ON fund_entries(reversal_of_id) WHERE reversal_of_id <> 0` 在插入层保证；
  插入前再只读复查一次是否已有冲销。并发重复冲销只有一笔插入成功，其余回滚并返回 409 / 40904，**绝不重复冲销、污染余额**。
- **幂等只入账一次**：预收/支出/冲销都要求 `idempotency_key`，唯一索引保证重复提交、网络重试、并发同键只有一笔入账，
  其余返回首次结果（响应 `replayed=true`）。前端使用 `crypto.randomUUID()` 生成。
- **重启一致**：余额是明细的物化缓存，权威余额始终可由 `SUM(delta_cents)` 重算；
  `GET /fund/balance` 与账户列表同时返回物化值、重算值与 `consistent` 标记。

- **多实例首笔并发安全**：专案账户用 `INSERT ... ON CONFLICT (case_id, client_id) DO NOTHING` 创建，
  再 `FOR UPDATE` 回读唯一账户行。多个服务实例同时为一个尚无账户的案件记首笔预收/支出时，
  不会因唯一约束冲突使 PostgreSQL 事务进入 aborted 状态而返回内部错误——各请求各自成功、账户只建一个；
  同键重试仍只入账一次。

并发控制：写事务内对专案账户行 `SELECT ... FOR UPDATE` 串行化；拿到账户/原明细行锁后再次复查幂等键，
使跨实例的同键并发在锁等待结束后回放首笔而非报唯一冲突（SQLite 等无行锁方言由全局写互斥补充），
冲突后回查幂等键，跨进程最终以数据库唯一约束为准。相关表：`fund_accounts`、`fund_entries`。

### FundEntryType（prepayment/expense/reversal）出现位置

- 后端：`backend/internal/constants/fund.go`、`backend/internal/model/fund.go`、`backend/internal/repository/fund_repository.go`、
  `backend/internal/service/fund_service.go`、`backend/internal/handler/fund_handler.go`、`backend/internal/dto/dto_fund.go`、
  `backend/internal/constants/error_codes.go`、`backend/internal/constants/messages.go`、`backend/internal/constants/log_templates.go`、`database/init.sql`
- 前端：`frontend/src/constants/fund.ts`、`frontend/src/types/index.ts`、`frontend/src/api/fund.ts`、
  `frontend/src/stores/fundStore.ts`、`frontend/src/pages/FundLedger.tsx`

## License

MIT License
