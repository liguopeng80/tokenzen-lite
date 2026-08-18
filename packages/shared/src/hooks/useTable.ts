import { useState, useEffect, useCallback, useRef } from 'react';
import type { PaginatedData } from '../types';

export interface UseTableOptions<T> {
  fetchFn: (params: Record<string, unknown>) => Promise<PaginatedData<T>>;
  defaultPageSize?: number;
  autoFetch?: boolean;
  /** When any element changes, auto-reset to page 1 and re-fetch */
  deps?: unknown[];
}

export interface UseTableReturn<T> {
  dataSource: T[];
  loading: boolean;
  pagination: {
    current: number;
    pageSize: number;
    total: number;
    showSizeChanger: boolean;
    onChange: (page: number, pageSize: number) => void;
  };
  refresh: () => void;
  reset: () => void;
}

export function useTable<T>(options: UseTableOptions<T>): UseTableReturn<T> {
  const { fetchFn, defaultPageSize = 20, autoFetch = true, deps = [] } = options;
  const [dataSource, setDataSource] = useState<T[]>([]);
  const [loading, setLoading] = useState(false);
  const [current, setCurrent] = useState(1);
  const [pageSize, setPageSize] = useState(defaultPageSize);
  const [total, setTotal] = useState(0);
  const fetchRef = useRef(fetchFn);
  fetchRef.current = fetchFn;
  const requestIdRef = useRef(0);
  const isFirstRender = useRef(true);

  const fetchData = useCallback(
    async (page: number, size: number) => {
      const id = ++requestIdRef.current;
      setLoading(true);
      try {
        // 分页参数键名与后端契约一致（server/internal/api/bind.go 的 PageParams）
        const result = await fetchRef.current({ page, page_size: size });
        if (id !== requestIdRef.current) return;
        setDataSource(result.items ?? []);
        setTotal(result.total ?? 0);
      } catch {
        if (id !== requestIdRef.current) return;
        setDataSource([]);
        setTotal(0);
      } finally {
        if (id === requestIdRef.current) {
          setLoading(false);
        }
      }
    },
    [],
  );

  useEffect(() => {
    if (autoFetch) {
      fetchData(current, pageSize);
    }
  }, [current, pageSize, fetchData, autoFetch]);

  // When filter deps change, reset to page 1 and re-fetch
  useEffect(() => {
    if (isFirstRender.current) {
      isFirstRender.current = false;
      return;
    }
    if (autoFetch) {
      setCurrent(1);
      fetchData(1, pageSize);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  const handlePageChange = useCallback((page: number, size: number) => {
    setCurrent(page);
    setPageSize(size);
  }, []);

  const refresh = useCallback(() => {
    fetchData(current, pageSize);
  }, [fetchData, current, pageSize]);

  const reset = useCallback(() => {
    setCurrent(1);
    fetchData(1, pageSize);
  }, [fetchData, pageSize]);

  return {
    dataSource,
    loading,
    pagination: {
      current,
      pageSize,
      total,
      showSizeChanger: true,
      onChange: handlePageChange,
    },
    refresh,
    reset,
  };
}
