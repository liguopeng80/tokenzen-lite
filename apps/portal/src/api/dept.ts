import type {
  DeptAggDimension,
  DeptBudget,
  DeptCostReport,
  DeptMember,
  ManagedDepartment,
  PaginatedData,
} from '@token-zen/shared';
import { apiGet } from '@token-zen/shared/api';
import { httpClient } from './client';

/**
 * 部门负责人视图。访问范围由后端按 departments.owner_user_id 逐次校验，
 * 前端传入的 department_id 只是请求参数，不构成授权依据。
 */
export const deptApi = {
  /** 当前用户负责的部门；返回空数组表示不是任何部门的负责人。 */
  departments: () => apiGet<ManagedDepartment[]>(httpClient, '/dept/departments'),

  /** 本部门指定自然月的消费与预算对比，month 格式 YYYY-MM，省略则取当月。 */
  budget: (departmentId: number, month?: string) =>
    apiGet<DeptBudget>(httpClient, '/dept/budget', {
      params: { department_id: departmentId, month },
    }),

  /** 本部门费用明细，时间范围为 Unix 秒；省略则取最近 30 天。 */
  costReport: (
    departmentId: number,
    groupBy: DeptAggDimension,
    range?: { start_timestamp?: number; end_timestamp?: number },
  ) =>
    apiGet<DeptCostReport>(httpClient, '/dept/cost-report', {
      params: { department_id: departmentId, group_by: groupBy, ...range },
    }),

  /** 本部门成员及其余额与当月消费。 */
  members: (departmentId: number, params?: Record<string, unknown>) =>
    apiGet<PaginatedData<DeptMember>>(httpClient, '/dept/members', {
      params: { department_id: departmentId, ...params },
    }),
};
