# 泄洪闸门操作协调服务

## 项目目标

该服务用于水工控制室协调泄洪闸门的命令申请、安全联锁复核和单次开度调整。系统在同一闸门上只允许一条已通过复核且尚未结束的执行命令，任何冲突都必须被显式拒绝，不能覆盖已经占用的闸门。

## 使用角色

- 运行协调员：登记闸门并提交开度调整命令。
- 安全复核员：提交联锁结论，决定命令是否通过。
- 闸门操作员：凭执行令牌启动命令并登记执行结果。

## 核心实体

- `gate`：闸门及其当前开度、运行状态、最大开度和活动命令。
- `gate_command`：一次开度调整申请，包含目标开度、申请原因、状态和执行令牌。
- `interlock_decision`：安全复核结论，状态为 clear 或 blocked。
- `operation_plan`：一次跨上下游的联动调整计划，包含有序阶段列表；阶段状态由各自命令的真实终态派生。
- `plan_stage`：计划中的一个有序阶段，绑定目标闸门、目标开度和启动后创建的命令。

## 业务流程

### register-gate

运行协调员通过 `POST /gates` 登记闸门。服务校验标识、名称、最大开度和初始开度，写入空闲状态；重复标识返回稳定冲突错误，不覆盖原记录。

### request-gate-command

运行协调员通过 `POST /commands` 提交开度调整命令。服务读取闸门当前开度和限制，校验目标开度与申请信息，创建待复核命令并冻结起始开度。

### review-safety-interlock

安全复核员通过 `POST /commands/{id}/interlock` 提交复核结论。clear 结论会原子占用闸门并签发一次执行令牌；blocked 结论会把命令置为 rejected。其他命令先占用同一闸门时，当前 clear 结论返回冲突且不改变闸门。

### execute-gate-command

闸门操作员通过 `POST /commands/{id}/execute` 提交执行令牌。服务校验命令、令牌、闸门占用关系和目标开度，在临界区内完成 approved、executing、completed 或 aborted 的转换，并同步更新闸门状态。令牌在命令结束后失效，不能复用。

### create-operation-plan

运行协调员通过 `POST /plans` 登记联动调整计划。服务校验名称、申请人和有序阶段列表（闸门存在、目标开度在闸门限制内），写入 active 状态；此时不创建任何命令，也不占用任何闸门。

### advance-operation-plan

运行协调员通过 `POST /plans/{id}/advance` 推进计划。服务在临界区内读取上一阶段命令的真实终态：任一阶段失败返回 `plan_blocked`，全部完成返回 `plan_already_completed`，当前阶段未结束返回 `plan_stage_in_progress`。依赖全部真实完成后，按当时闸门条件重新确认下一阶段（闸门故障返回 `gate_faulted`，目标开度已被现场达到返回 `plan_condition_changed`），为下一阶段创建待复核命令并冻结起始开度。新命令随后走既有复核与执行流程，阶段完成事实以命令终态为准。

## 状态与规则

- `gate` 状态：idle、reserved、moving、faulted。
- `gate_command` 状态：pending、approved、rejected、executing、completed、aborted。
- `operation_plan` 状态：active、blocked、completed，由阶段命令终态派生。
- `plan_stage` 状态：waiting、active、completed、failed，由所绑定命令的真实状态派生。
- 只有 pending 命令可以复核。
- 只有 approved 命令可以执行，且执行令牌必须匹配。
- clear 复核与闸门占用必须同时成功；任一步失败时两者都不写入。
- blocked 复核使命令 rejected，不会占用闸门。
- 模拟故障会令命令 aborted、闸门 faulted，并保留最终开度。
- 已完成或已中止的命令不能再次执行。
- 计划阶段按顺序推进，只有上一阶段命令真实完成后才能启动下一阶段。
- 阶段启动时才创建命令，waiting 阶段不持有命令也不占用闸门。
- 已完成阶段是单向事实，后续阶段阻塞或失败不会回写它。

## 模块边界

- `cmd/server`：进程入口、监听地址和退出控制。
- `internal/httpapi`：HTTP 路由、JSON 解码、状态码映射。
- `internal/coordination`：工作流编排、命令标识和执行令牌签发。
- `internal/domain`：实体、不变量和状态转换规则。
- `internal/storage`：带互斥保护的内存仓库和原子写入。
- `internal/contracts`：API 请求、响应和稳定错误结构。
- `internal/bootstrap`：运行时依赖装配。

## 接口

- `GET /health`
- `POST /gates`
- `GET /gates/{id}`
- `POST /commands`
- `GET /commands/{id}`
- `POST /commands/{id}/interlock`
- `POST /commands/{id}/execute`
- `POST /plans`
- `GET /plans/{id}`
- `POST /plans/{id}/advance`

错误响应统一为：

```json
{"code":"stable_error_code","message":"可读说明"}
```

## 验证计划

本初始化基线刻意不生成单元测试、夹具或浏览器测试文件，也不声明 `test_command`。后续任务阶段负责补充测试，并在该阶段建立红绿验证。当前每条工作流通过 `cmd/workflowcheck` 启动真实 HTTP 服务并调用公开接口，校验成功状态、持久化结果和关键失败分支。
