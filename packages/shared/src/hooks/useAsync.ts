import { useCallback, useEffect, useRef, useState } from 'react';

export interface UseAsyncResult<T> {
  /** 最近一次成功的结果；尚未成功或最近一次失败时为 null。 */
  data: T | null;
  loading: boolean;
  error: Error | null;
  /** 手动重新拉取（依赖未变时也强制刷新）。 */
  refresh: () => void;
}

/**
 * useAsync 收口「请求 → loading → data」的样板：竞态守卫（仅最后一次请求的结果生效）、
 * deps 变化自动重拉、失败时 data 置 null 且 error 记录原因。调用方按需展示 error。
 *
 * fetchFn 通过 ref 持有最新闭包，因此 run 引用稳定；重拉由 deps 数组触发，
 * 与 useTable 同一模式。deps 为空数组时仅在挂载时拉取一次。
 */
export function useAsync<T>(fetchFn: () => Promise<T>, deps: unknown[]): UseAsyncResult<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const requestIdRef = useRef(0);
  const fetchRef = useRef(fetchFn);
  fetchRef.current = fetchFn;

  const run = useCallback(async () => {
    const id = ++requestIdRef.current;
    setLoading(true);
    setError(null);
    try {
      const result = await fetchRef.current();
      if (id !== requestIdRef.current) return; // 已被更新的请求取代，丢弃本次结果。
      setData(result);
    } catch (err) {
      if (id !== requestIdRef.current) return;
      setError(err instanceof Error ? err : new Error(String(err)));
      setData(null);
    } finally {
      if (id === requestIdRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void run();
    // run 引用稳定（useCallback 空依赖），重拉由 deps 触发。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { data, loading, error, refresh: run };
}
