import { describe, it, expect, afterEach } from 'vitest';
import axios, { type InternalAxiosRequestConfig } from 'axios';
import { httpClient } from './client';
import { userApi } from './users';

const originalAdapter = httpClient.defaults.adapter;

afterEach(() => {
  httpClient.defaults.adapter = originalAdapter;
});

describe('管理端列表分页参数透传', () => {
  it('list({ page: 2 }) 发出的请求查询串应包含 page=2', async () => {
    let requestUri = '';
    httpClient.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
      requestUri = axios.getUri(config);
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

    await userApi.list({ page: 2, page_size: 20 });

    expect(requestUri).toContain('page=2');
    expect(requestUri).toContain('page_size=20');
  });
});
