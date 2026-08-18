import type { ModelImportItem } from '@/api/models';

/**
 * 把自定义导入内容解析为条目数组，并做提交前的前置校验。
 * 返回的第二个值非空表示解析或校验失败的原因。
 *
 * 校验 price 存在性的原因：无定价的模型导入后直接进入对外目录，
 * 调用会被零扣费；在提交前拦下并指明具体条目，省去逐条排查。
 */
export function parseImportJSON(raw: string): [ModelImportItem[], string] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [[], '不是合法的 JSON'];
  }
  const items = Array.isArray(parsed) ? parsed : (parsed as { items?: unknown })?.items;
  if (!Array.isArray(items) || items.length === 0) {
    return [[], '需要一个 items 数组，或直接是条目数组'];
  }
  for (const item of items) {
    const it = item as Partial<ModelImportItem>;
    if (!it || typeof it.name !== 'string' || !it.name) {
      return [[], '每个条目都需要非空的 name 字段'];
    }
    if (!it.price || typeof it.price !== 'object') {
      return [[], `条目 ${it.name} 缺少 price：无定价的模型上架后会被零扣费调用`];
    }
  }
  return [items as ModelImportItem[], ''];
}
