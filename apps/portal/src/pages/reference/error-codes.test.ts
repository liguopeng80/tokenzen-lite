import { describe, it, expect } from 'vitest';
import {
  RELAY_ERROR_CODES,
  RelayErrorCode,
  RelayErrorByCode,
} from '@token-zen/shared/constants';

/**
 * 错误码常量表与 docs/api-contract.md「错误码表」的一致性校验。
 *
 * api-contract.md 是码值、HTTP、含义、客户端建议动作的权威来源。本测试把契约里的固定码值集合
 * 冻结成快照，断言 shared 常量表与之一致：有人改动常量表（增删改码值）时必须同步更新契约与这里，
 * 否则前端展示与文档漂移、用户按错误的动作自助诊断。
 *
 * 透传类「上游原状态码 / 上游原业务码」不是固定码，不在断言集合内。
 */
describe('RELAY_ERROR_CODES 与 api-contract 错误码表一致', () => {
  // 取自 docs/api-contract.md 错误码表的固定业务码集合（不含透传项）。
  const contractCodes = new Set<string>([
    'invalid_api_key',
    'key_disabled',
    'key_expired',
    'key_unavailable',
    'ip_not_allowed',
    'user_disabled',
    'model_not_allowed',
    'invalid_request_error',
    'unsupported_feature',
    'model_endpoint_mismatch',
    'model_provider_mismatch',
    'model_not_found',
    'provider_not_found',
    'insufficient_credits',
    'key_quota_exceeded',
    'daily_spend_limit_exceeded',
    'key_daily_spend_limit_exceeded',
    'rate_limited',
    'upstream_error',
    'overloaded',
    'no_channel',
    'channel_protocol_unsupported',
    'channel_error',
    'model_not_priced',
    'internal_error',
  ]);

  it('码值集合与契约完全一致（无增删）', () => {
    const tableCodes = new Set(RELAY_ERROR_CODES.map((d) => d.code));
    expect(tableCodes).toEqual(contractCodes);
  });

  it('RelayErrorCode 常量枚举覆盖全部码值', () => {
    const enumValues = new Set(Object.values(RelayErrorCode));
    expect(enumValues).toEqual(contractCodes);
  });

  it('码值无重复', () => {
    const codes = RELAY_ERROR_CODES.map((d) => d.code);
    expect(codes.length).toBe(new Set(codes).size);
  });

  it('每条码值 HTTP 落在合理区间且含义/动作非空', () => {
    for (const def of RELAY_ERROR_CODES) {
      expect(def.http).toBeGreaterThanOrEqual(400);
      expect(def.http).toBeLessThan(600);
      expect(def.meaning.length).toBeGreaterThan(0);
      expect(def.action.length).toBeGreaterThan(0);
    }
  });

  it('RelayErrorByCode 索引覆盖全表', () => {
    for (const def of RELAY_ERROR_CODES) {
      expect(RelayErrorByCode[def.code]).toBe(def);
    }
  });

  it('关键 HTTP 状态与码值绑定稳定（契约约定）', () => {
    const byCode = (c: string) => RELAY_ERROR_CODES.find((d) => d.code === c)?.http;
    expect(byCode('invalid_api_key')).toBe(401);
    expect(byCode('model_not_found')).toBe(404);
    expect(byCode('insufficient_credits')).toBe(402);
    expect(byCode('rate_limited')).toBe(429);
    expect(byCode('upstream_error')).toBe(502);
    expect(byCode('no_channel')).toBe(503);
    expect(byCode('overloaded')).toBe(503);
    expect(byCode('internal_error')).toBe(500);
  });
});
