/**
 * errorMessageOf 提取后端响应信封里的业务原因（如「部门仍有成员」），
 * 取不到时回退到调用方给的兜底文案。
 *
 * 直接展示后端消息而非统一文案，是为了让管理员看到「为什么不行」而不是「不行」——
 * 后端的拒绝原因已经是面向管理员的完整句子。
 */
export function errorMessageOf(err: unknown, fallback: string): string {
  const data = (err as { response?: { data?: { message?: string } } })?.response?.data;
  return data?.message || fallback;
}
