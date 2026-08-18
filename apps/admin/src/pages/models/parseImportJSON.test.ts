import { describe, it, expect } from 'vitest';
import { parseImportJSON } from './importParse';

/**
 * 自定义导入内容的解析与前置校验。
 * 业务后果：缺 price 的条目若被放行，模型上架后会被零扣费调用；
 * 这里在提交前拦下，并给出具体到条目名称的原因，避免管理员逐条排查。
 */
describe('parseImportJSON', () => {
  const item = { name: 'gpt-4o', price: { input_price: 1000 } };

  it('S1: 接受 {items: [...]} 形态', () => {
    const [items, err] = parseImportJSON(JSON.stringify({ items: [item] }));
    expect(err).toBe('');
    expect(items).toHaveLength(1);
    expect(items[0].name).toBe('gpt-4o');
  });

  it('S2: 接受直接的条目数组', () => {
    const [items, err] = parseImportJSON(JSON.stringify([item]));
    expect(err).toBe('');
    expect(items).toHaveLength(1);
  });

  it('S3: 非法 JSON 报错且不返回条目', () => {
    const [items, err] = parseImportJSON('{items: }');
    expect(items).toHaveLength(0);
    expect(err).toContain('JSON');
  });

  it('S4: 空数组与非数组都拒绝', () => {
    expect(parseImportJSON('[]')[1]).not.toBe('');
    expect(parseImportJSON(JSON.stringify({ items: {} }))[1]).not.toBe('');
  });

  it('S5: 缺少 name 的条目拒绝', () => {
    const [, err] = parseImportJSON(JSON.stringify([{ price: { input_price: 1 } }]));
    expect(err).toContain('name');
  });

  it('S6: 缺少 price 的条目拒绝，并指明是哪个模型', () => {
    const [, err] = parseImportJSON(JSON.stringify([{ name: 'claude-x' }]));
    expect(err).toContain('claude-x');
    expect(err).toContain('price');
  });
});
