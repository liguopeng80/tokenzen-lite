// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import axios, { type InternalAxiosRequestConfig } from 'axios';
import { useTable } from '@token-zen/shared/hooks';
import type { PaginatedData } from '@token-zen/shared/types';
import { httpClient } from './api/client';
import { userApi } from './api/users';

function pageData<T>(page: number, items: T[], total = items.length): PaginatedData<T> {
  return { page, page_size: 20, total, items };
}

describe('useTable 单元行为', () => {
  it('挂载后自动以 { page: 1, page_size: 20 } 拉取，dataSource 与 total 来自返回值', async () => {
    const fetchFn = vi
      .fn()
      .mockResolvedValue(pageData(1, ['a', 'b'], 42));
    const { result } = renderHook(() => useTable({ fetchFn }));

    await waitFor(() => {
      expect(result.current.dataSource).toEqual(['a', 'b']);
    });
    expect(fetchFn).toHaveBeenCalledTimes(1);
    expect(fetchFn).toHaveBeenCalledWith({ page: 1, page_size: 20 });
    expect(result.current.pagination.total).toBe(42);
    expect(result.current.loading).toBe(false);
  });

  it('pagination.onChange(2, 20) 触发 { page: 2, page_size: 20 } 拉取并替换 dataSource', async () => {
    const fetchFn = vi
      .fn()
      .mockImplementation(({ page }: { page: number }) =>
        Promise.resolve(page === 1 ? pageData(1, ['p1'], 40) : pageData(2, ['p2'], 40)),
      );
    const { result } = renderHook(() => useTable({ fetchFn }));
    await waitFor(() => expect(result.current.dataSource).toEqual(['p1']));

    act(() => {
      result.current.pagination.onChange(2, 20);
    });

    await waitFor(() => expect(result.current.dataSource).toEqual(['p2']));
    expect(fetchFn).toHaveBeenLastCalledWith({ page: 2, page_size: 20 });
    expect(result.current.pagination.current).toBe(2);
  });

  it('deps 变化后重置到第 1 页重新拉取', async () => {
    const fetchFn = vi
      .fn()
      .mockImplementation(({ page }: { page: number }) =>
        Promise.resolve(pageData(page, [`page-${page}`], 60)),
      );
    const { result, rerender } = renderHook(
      ({ keyword }: { keyword: string }) => useTable({ fetchFn, deps: [keyword] }),
      { initialProps: { keyword: 'a' } },
    );
    await waitFor(() => expect(result.current.dataSource).toEqual(['page-1']));

    act(() => {
      result.current.pagination.onChange(2, 20);
    });
    await waitFor(() => expect(result.current.pagination.current).toBe(2));
    await waitFor(() => expect(result.current.dataSource).toEqual(['page-2']));

    rerender({ keyword: 'b' });

    await waitFor(() => expect(result.current.pagination.current).toBe(1));
    await waitFor(() => expect(result.current.dataSource).toEqual(['page-1']));
    expect(fetchFn).toHaveBeenLastCalledWith({ page: 1, page_size: 20 });
  });

  it('fetchFn 抛错时 dataSource 为空、total 为 0、loading 收敛为 false', async () => {
    const fetchFn = vi.fn().mockRejectedValue(new Error('网络错误'));
    const { result } = renderHook(() => useTable({ fetchFn }));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(fetchFn).toHaveBeenCalled();
    expect(result.current.dataSource).toEqual([]);
    expect(result.current.pagination.total).toBe(0);
  });
});

describe('跨层 seam：useTable → userApi.list → HTTP 查询串', () => {
  const originalAdapter = httpClient.defaults.adapter;
  afterEach(() => {
    httpClient.defaults.adapter = originalAdapter;
  });

  it('onChange(2, 20) 后实际请求查询串包含 page=2，dataSource 来自第 2 页返回', async () => {
    const uris: string[] = [];
    httpClient.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
      const uri = axios.getUri(config);
      uris.push(uri);
      const isPage2 = uri.includes('page=2');
      return {
        data: {
          success: true,
          message: '',
          data: isPage2
            ? { page: 2, page_size: 20, total: 21, items: [{ id: 21, username: 'u21' }] }
            : { page: 1, page_size: 20, total: 21, items: [{ id: 1, username: 'u1' }] },
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    const { result } = renderHook(() =>
      useTable({ fetchFn: (params) => userApi.list(params) }),
    );
    await waitFor(() => expect(result.current.dataSource).toHaveLength(1));

    act(() => {
      result.current.pagination.onChange(2, 20);
    });

    await waitFor(() =>
      expect(result.current.dataSource).toEqual([{ id: 21, username: 'u21' }]),
    );
    expect(uris[uris.length - 1]).toContain('page=2');
    expect(uris[uris.length - 1]).toContain('page_size=20');
  });
});
