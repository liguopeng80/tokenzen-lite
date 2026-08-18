import { describe, it, expect } from 'vitest';
import { resolveBaseUrl, isBaseUrlInferred } from './useBaseUrl';

/**
 * 接入指引 Base URL 的取值优先级。
 * 背景：旧实现探测后端并不存在的 GET /api/status，每次进接入指引页都有一次注定失败的
 * 请求，Base URL 实际总是回落到浏览器站点地址；生产环境 /v1 挂独立域名时该地址是错的，
 * 用户按指引配置客户端必然连不上。改为读管理员配置的对外 API 基址。
 */
describe('resolveBaseUrl', () => {
  it('S1: 管理员配置了对外 API 基址时优先采用', () => {
    expect(resolveBaseUrl('https://api.example.com', 'https://env.example.com', 'https://portal.example.com'))
      .toBe('https://api.example.com');
  });

  it('S2: 配置值末尾的斜杠被去除，避免拼出 //v1', () => {
    expect(resolveBaseUrl('https://api.example.com/', undefined, 'https://portal.example.com'))
      .toBe('https://api.example.com');
  });

  it('S3: 未配置时回落到构建期注入的 VITE_API_BASE_URL', () => {
    expect(resolveBaseUrl('   ', 'https://env.example.com', 'https://portal.example.com'))
      .toBe('https://env.example.com');
  });

  it('S4: 两者都没有时用浏览器当前站点地址', () => {
    expect(resolveBaseUrl('', undefined, 'https://portal.example.com'))
      .toBe('https://portal.example.com');
  });

  it('S5: 本机开发时把站点端口改写为后端端口', () => {
    expect(resolveBaseUrl('', undefined, 'http://localhost:19074')).toBe('http://localhost:19030');
    expect(resolveBaseUrl('', undefined, 'http://127.0.0.1:19074')).toBe('http://127.0.0.1:19030');
  });
});

/**
 * 是否需要提示「地址靠推断」。容器或反向代理部署下推断值通常不是真实 API 入口，
 * 用户逐字照抄仍连不通，因此指引页必须显式提示。
 */
describe('isBaseUrlInferred', () => {
  it('S1: 管理员已配置对外基址时不提示', () => {
    expect(isBaseUrlInferred('https://api.example.com', undefined)).toBe(false);
  });

  it('S2: 构建期注入了基址时不提示', () => {
    expect(isBaseUrlInferred('  ', 'https://env.example.com')).toBe(false);
  });

  it('S3: 两者都没有、只能按浏览器地址推断时提示', () => {
    expect(isBaseUrlInferred('', undefined)).toBe(true);
    expect(isBaseUrlInferred('   ', '')).toBe(true);
  });
});
