// 厂商「Coding Plan」（编程套餐）目录常量。与 ProviderCatalog 同模式：静态只读目录，
// 随版本叠加演进（只增不删）。权威定义见 docs/glossary.md 的 Provider 节。
//
// 重要：Coding Plan 使用**专用端点**，与厂商通用 API 端点不同（如智谱 Coding Plan 的
// OpenAI 端点是 /api/coding/paas/v4 而非通用的 /api/paas/v4；Kimi Coding Plan 用独立域名
// api.kimi.com/coding 而非 api.moonshot.cn）。这正是「端点分化」——新套餐启用新端点，
// 旧端点继续服务旧套餐，二者并存。
//
// 端点来源：智谱、Kimi、MiniMax 三家均取自厂商官方 Coding Plan 文档。
// base_url 按本仓约定：openai_compat 含版本根路径（/v1、/coding/v1、/api/coding/paas/v4 等），
// 由后端 openai_compat codec 追加 /chat/completions；anthropic 为其根，codec 追加 /v1/messages。
//
// 模型清单为 starter，随厂商发布手工维护；既有渠道不自动跟随目录变化（符合「按套餐创建」
// 功能收窄范围：创建时快照，后续由管理员在编辑抽屉里增删）。

import type { Provider, ChannelProtocol } from '../types';

export interface PackageEndpoint {
  protocol: ChannelProtocol;
  /** 含版本根路径（openai_compat 需 /v1 或 /paas/v4）；anthropic 为其根 */
  base_url: string;
}

export interface PackageMeta {
  /** 'zhipu-coding-plan' */
  id: string;
  /** '智谱 Coding Plan' */
  name: string;
  provider: Provider;
  description: string;
  /** 套餐/密钥获取页 */
  website_url: string;
  /** 套餐模型清单（创建时快照，跨协议共享；starter，需随厂商更新手工维护） */
  models: string[];
  /** 双协议套餐 2 条，单协议 1 条 */
  endpoints: PackageEndpoint[];
}

export const PackageCatalog: PackageMeta[] = [
  {
    id: 'zhipu-coding-plan',
    name: '智谱 Coding Plan',
    provider: 'zhipu',
    description: '智谱面向编程客户端的套餐，一个 Key 同时暴露 OpenAI 兼容与 Anthropic 两套端点。',
    website_url: 'https://open.bigmodel.cn',
    // 套餐当前开放的模型（随厂商发布在此手工增删；须与上游 API 的 model id 一致）。
    models: ['GLM-5.2', 'GLM-4.7'],
    endpoints: [
      { protocol: 'openai_compat', base_url: 'https://open.bigmodel.cn/api/coding/paas/v4' },
      { protocol: 'anthropic', base_url: 'https://open.bigmodel.cn/api/anthropic' },
    ],
  },
  {
    id: 'kimi-coding-plan',
    name: 'Kimi Coding Plan',
    provider: 'moonshot',
    description: 'Moonshot Kimi 面向编程客户端的套餐，一个 Key 同时暴露 OpenAI 兼容与 Anthropic 两套端点。',
    website_url: 'https://platform.moonshot.cn',
    // 套餐当前开放的模型（随厂商发布在此手工增删；须与上游 API 的 model id 一致）。
    models: ['k3', 'k3-256k', 'kimi-for-coding'],
    endpoints: [
      { protocol: 'openai_compat', base_url: 'https://api.kimi.com/coding/v1' },
      { protocol: 'anthropic', base_url: 'https://api.kimi.com/coding' },
    ],
  },
  {
    id: 'minimax-coding-plan',
    name: 'MiniMax Coding Plan',
    provider: 'minimax',
    description: 'MiniMax 面向编程客户端的套餐，一个 Key 同时暴露 OpenAI 兼容与 Anthropic 两套端点。',
    website_url: 'https://platform.minimaxi.com',
    // 套餐当前开放的模型（随厂商发布在此手工增删；须与上游 API 的 model id 一致）。
    models: ['MiniMax-M3', 'MiniMax-M2.7'],
    endpoints: [
      { protocol: 'openai_compat', base_url: 'https://api.minimaxi.com/v1' },
      { protocol: 'anthropic', base_url: 'https://api.minimaxi.com/anthropic' },
    ],
  },
];
