import { describe, it, expect, afterEach } from 'vitest';
import axios, { type InternalAxiosRequestConfig } from 'axios';
import { httpClient } from './client';
import { userApi } from './users';
import { channelApi } from './channels';
import { modelApi } from './models';
import { redemptionApi, ledgerApi } from './billing';
import { usageLogApi } from './usageLogs';

const originalAdapter = httpClient.defaults.adapter;

afterEach(() => {
  httpClient.defaults.adapter = originalAdapter;
});

/** 注入捕获型 adapter，返回空的第 2 页分页数据 */
function installCaptureAdapter() {
  const captured: { uri: string; path: string }[] = [];
  httpClient.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
    captured.push({
      uri: axios.getUri(config),
      path: config.url ?? '',
    });
    return {
      data: {
        success: true,
        message: '',
        data: { page: 2, page_size: 20, total: 0, items: [] },
      },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    };
  };
  return captured;
}

describe('管理端六个列表函数分页参数透传（表驱动）', () => {
  const cases: {
    name: string;
    call: () => Promise<unknown>;
    expectedPath: string;
  }[] = [
    {
      name: 'userApi.list',
      call: () => userApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/admin/users/',
    },
    {
      name: 'channelApi.list',
      call: () => channelApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/admin/channels/',
    },
    {
      name: 'modelApi.list',
      call: () => modelApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/admin/models/',
    },
    {
      name: 'redemptionApi.list',
      call: () => redemptionApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/admin/redemptions/',
    },
    {
      name: 'ledgerApi.list',
      call: () => ledgerApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/admin/ledger',
    },
    {
      name: 'usageLogApi.list',
      call: () => usageLogApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/admin/usage-logs',
    },
  ];

  it.each(cases)('$name 的请求查询串包含 page=2 且路径正确', async ({ call, expectedPath }) => {
    const captured = installCaptureAdapter();
    await call();
    expect(captured).toHaveLength(1);
    expect(captured[0].uri).toContain('page=2');
    expect(captured[0].uri).toContain('page_size=20');
    // 旧适配层会把 page 改写为 p=；确认无残留
    expect(captured[0].uri).not.toMatch(/[?&]p=/);
    expect(captured[0].path).toBe(expectedPath);
  });
});

describe('管理端过滤参数与分页参数合并透传', () => {
  it('userApi.list 同时携带 page、keyword、role', async () => {
    const captured = installCaptureAdapter();
    // 对应 apps/admin/src/pages/users/index.tsx 页面层的参数组合方式
    await userApi.list({ page: 2, page_size: 20, keyword: 'abc', role: 'admin' });
    expect(captured[0].uri).toContain('page=2');
    expect(captured[0].uri).toContain('page_size=20');
    expect(captured[0].uri).toContain('keyword=abc');
    expect(captured[0].uri).toContain('role=admin');
  });
});
