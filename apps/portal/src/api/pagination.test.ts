import { describe, it, expect, afterEach } from 'vitest';
import axios, { type InternalAxiosRequestConfig } from 'axios';
import { httpClient } from './client';
import { keysApi } from './keys';
import { ledgerApi, usageApi } from './usage';

const originalAdapter = httpClient.defaults.adapter;

afterEach(() => {
  httpClient.defaults.adapter = originalAdapter;
});

function installCaptureAdapter() {
  const captured: { uri: string; path: string }[] = [];
  httpClient.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
    captured.push({ uri: axios.getUri(config), path: config.url ?? '' });
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

describe('用户端列表分页参数透传', () => {
  const cases: {
    name: string;
    call: () => Promise<unknown>;
    expectedPath: string;
  }[] = [
    {
      name: 'keysApi.list',
      call: () => keysApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/me/keys/',
    },
    {
      name: 'ledgerApi.list',
      call: () => ledgerApi.list({ page: 2, page_size: 20 }),
      expectedPath: '/me/ledger',
    },
    {
      name: 'usageApi.logs',
      call: () => usageApi.logs({ page: 2, page_size: 20 }),
      expectedPath: '/me/usage-logs',
    },
  ];

  it.each(cases)('$name 的请求查询串包含 page=2 且路径正确', async ({ call, expectedPath }) => {
    const captured = installCaptureAdapter();
    await call();
    expect(captured).toHaveLength(1);
    expect(captured[0].uri).toContain('page=2');
    expect(captured[0].uri).toContain('page_size=20');
    expect(captured[0].uri).not.toMatch(/[?&]p=/);
    expect(captured[0].path).toBe(expectedPath);
  });
});
