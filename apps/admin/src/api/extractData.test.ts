import { describe, it, expect } from 'vitest';
import { extractData, backendErrorMessage } from '@token-zen/shared/api';
import type { ApiResponse } from '@token-zen/shared/types';

describe('extractData 统一信封解析', () => {
  it('success=true 时返回 data 原值', () => {
    const payload = { page: 1, page_size: 20, total: 3, items: [1, 2, 3] };
    const response = {
      data: {
        success: true,
        message: '',
        data: payload,
      } as ApiResponse<typeof payload>,
    };
    expect(extractData(response)).toBe(payload);
  });

  it('success=false 时抛出携带后端 message 的 Error', () => {
    const response = {
      data: {
        success: false,
        message: '积分不足',
        data: null,
      } as ApiResponse<null>,
    };
    expect(() => extractData(response)).toThrowError('积分不足');
  });
});

describe('backendErrorMessage 提取 HTTP 错误响应中的拒绝原因', () => {
  it('4xx 响应体带 message 时返回该中文说明', () => {
    const err = {
      message: 'Request failed with status code 400',
      response: {
        status: 400,
        data: { success: false, message: '用户名须为 3-32 位字母、数字、下划线或连字符', data: null },
      },
    };
    expect(backendErrorMessage(err)).toBe('用户名须为 3-32 位字母、数字、下划线或连字符');
  });

  it('响应体没有 message 时返回 null，由调用方保留原有文案', () => {
    const err = { message: 'Network Error', response: { status: 502, data: { success: false, data: null } } };
    expect(backendErrorMessage(err)).toBeNull();
  });

  it('message 为空串时返回 null', () => {
    const err = { response: { status: 400, data: { success: false, message: '   ', data: null } } };
    expect(backendErrorMessage(err)).toBeNull();
  });

  it('无响应体（网络中断、下载流）时返回 null', () => {
    expect(backendErrorMessage({ message: 'timeout of 30000ms exceeded' })).toBeNull();
    expect(backendErrorMessage({ response: { status: 500, data: 'plain text body' } })).toBeNull();
    expect(backendErrorMessage(undefined)).toBeNull();
  });
});
