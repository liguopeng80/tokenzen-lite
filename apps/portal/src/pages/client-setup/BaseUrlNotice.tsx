import { Alert } from 'antd';
import { useBaseUrlInfo } from './useBaseUrl';

/**
 * 未配置对外 API 基址时的提示。
 *
 * 三态由 useBaseUrlInfo 决定：管理员已配置基址或构建期注入了基址时不渲染；
 * 只有落到「按浏览器当前地址推断」时才提示。
 */
function BaseUrlNotice() {
  const { baseUrl, inferred } = useBaseUrlInfo();
  if (!inferred) return null;

  return (
    <Alert
      type="warning"
      showIcon
      style={{ marginBottom: 16 }}
      message="以下地址是按你当前浏览器地址推断的，未必是真实的 API 入口"
      description={
        <>
          管理员尚未在系统设置中填写对外 API 基址，因此本页展示的 <code>{baseUrl}</code>{' '}
          取自浏览器当前地址。若系统部署在容器或反向代理之后，API 入口通常与门户地址不同，
          照此配置会连接失败。连不通时请联系管理员确认应使用的地址。
        </>
      }
    />
  );
}

export default BaseUrlNotice;
