import type {
  AlertEvent,
  AuditLog,
  BatchGrantSummary,
  BatchStatusSummary,
  CostReport,
  CostReportDimension,
  Department,
  DepartmentBudgetReport,
  DepartmentStatus,
  DepartmentWithStats,
  HeatmapResponse,
  PaginatedData,
  Project,
  ProjectBudgetReport,
  ProjectStatus,
  ProjectWithStats,
  UserImportSummary,
  UserStatus,
} from '@token-zen/shared';
import { apiGet, apiPost, apiPut, apiDelete } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface DepartmentPayload {
  name: string;
  code?: string;
  owner_user_id?: number | null;
  monthly_budget_credits?: number;
  allowed_models?: string[];
  status?: DepartmentStatus;
  note?: string;
}

export const departmentApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<DepartmentWithStats>>(httpClient, '/admin/departments/', { params }),

  /** 下拉选择用：一次取足，部门数量远小于分页上限 */
  options: () =>
    apiGet<PaginatedData<DepartmentWithStats>>(httpClient, '/admin/departments/', {
      params: { page: 1, page_size: 100 },
    }),

  getById: (id: number) => apiGet<Department>(httpClient, `/admin/departments/${id}`),

  create: (data: DepartmentPayload) =>
    apiPost<Department>(httpClient, '/admin/departments/', data),

  update: (id: number, data: DepartmentPayload) =>
    apiPut<void>(httpClient, `/admin/departments/${id}`, data),

  delete: (id: number) => apiDelete<void>(httpClient, `/admin/departments/${id}`),

  /** remove 为 true 时把这些用户转为未分配部门 */
  setMembers: (id: number, userIds: number[], remove = false) =>
    apiPost<{ affected: number }>(httpClient, `/admin/departments/${id}/members`, {
      user_ids: userIds,
      remove,
    }),
};

export interface ProjectPayload {
  name: string;
  code?: string;
  owner_user_id?: number | null;
  monthly_budget_credits?: number;
  status?: ProjectStatus;
  note?: string;
  external_ref?: string;
  idempotency_key?: string;
}

export const projectApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<ProjectWithStats>>(httpClient, '/admin/projects/', { params }),

  /** 下拉选择用：一次取足，项目数量远小于分页上限 */
  options: () =>
    apiGet<PaginatedData<ProjectWithStats>>(httpClient, '/admin/projects/', {
      params: { page: 1, page_size: 100 },
    }),

  getById: (id: number) => apiGet<Project>(httpClient, `/admin/projects/${id}`),

  getByExternalRef: (ref: string) =>
    apiGet<Project>(httpClient, `/admin/projects/external/${ref}`),

  create: (data: ProjectPayload) =>
    apiPost<Project>(httpClient, '/admin/projects/', data),

  update: (id: number, data: ProjectPayload) =>
    apiPut<void>(httpClient, `/admin/projects/${id}`, data),

  delete: (id: number) => apiDelete<void>(httpClient, `/admin/projects/${id}`),
};

export interface AuditLogQuery extends Record<string, unknown> {
  action?: string;
  target_type?: string;
  target_id?: number;
  operator_id?: number;
  result?: string;
  keyword?: string;
  start_timestamp?: number;
  end_timestamp?: number;
  page?: number;
  page_size?: number;
}

export const auditApi = {
  list: (params?: AuditLogQuery) =>
    apiGet<PaginatedData<AuditLog>>(httpClient, '/admin/audit-logs/', { params }),

  /** 动作枚举由后端下发，避免前端硬编码一份会与后端漂移的清单 */
  actions: () => apiGet<string[]>(httpClient, '/admin/audit-logs/actions'),
};

export const alertApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<AlertEvent>>(httpClient, '/admin/alerts/', { params }),

  /** 向当前配置的通道发一条测试消息，同步返回投递结果 */
  test: () => apiPost<AlertEvent>(httpClient, '/admin/alerts/test', {}),
};

export interface CostReportQuery extends Record<string, unknown> {
  group_by?: CostReportDimension;
  start_timestamp?: number;
  end_timestamp?: number;
  user_id?: number;
  department_id?: number;
  project_id?: number;
  model?: string;
  channel_id?: number;
  api_key_id?: number;
}

export const reportApi = {
  cost: (params?: CostReportQuery) =>
    apiGet<CostReport>(httpClient, '/admin/stats/cost-report', { params }),

  /** month 形如 2026-08，缺省为当月 */
  departmentBudget: (month?: string) =>
    apiGet<DepartmentBudgetReport>(httpClient, '/admin/stats/department-budget', {
      params: month ? { month } : undefined,
    }),

  /** month 形如 2026-08，缺省为当月 */
  projectBudget: (month?: string) =>
    apiGet<ProjectBudgetReport>(httpClient, '/admin/stats/project-budget', {
      params: month ? { month } : undefined,
    }),

  heatmap: (params?: HeatmapQuery) =>
    apiGet<HeatmapResponse>(httpClient, '/admin/stats/heatmap', { params }),
};

export interface HeatmapQuery extends Record<string, unknown> {
  start_timestamp?: number;
  end_timestamp?: number;
  user_id?: number;
  model?: string;
  channel_id?: number;
  department_id?: number;
}

export interface UserImportItem {
  username: string;
  password: string;
  display_name?: string;
  email?: string;
  department_id?: number | null;
  initial_credits?: number;
}

export const batchApi = {
  importUsers: (items: UserImportItem[], defaultDepartmentId?: number | null) =>
    apiPost<UserImportSummary>(httpClient, '/admin/users/import', {
      items,
      default_department_id: defaultDepartmentId ?? null,
    }),

  /**
   * 批量发放积分。userIds 与 departmentId 二选一；
   * idempotencyKey 非空时重复提交只记一次账。
   */
  grantCredits: (payload: {
    user_ids?: number[];
    department_id?: number | null;
    amount: number;
    note?: string;
    idempotency_key?: string;
  }) => apiPost<BatchGrantSummary>(httpClient, '/admin/credits/batch-grant', payload),

  /**
   * 批量启用或禁用用户，用于离职与调岗的集中处置。
   * user_ids 与 department_id 二选一；逐条处理，单条失败不影响其余账号。
   */
  setUserStatus: (payload: {
    user_ids?: number[];
    department_id?: number | null;
    status: UserStatus;
  }) => apiPost<BatchStatusSummary>(httpClient, '/admin/users/batch-status', payload),
};
