import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { useAuthStore, useNotificationStore } from '@/stores';
import { copyToClipboard } from '@/utils/clipboard';
import { normalizeApiBase } from '@/utils/connection';
import styles from './QuickStartPage.module.scss';

export function QuickStartPage() {
  const { t } = useTranslation();
  const apiBase = useAuthStore((state) => state.apiBase);
  const showNotification = useNotificationStore((state) => state.showNotification);
  let gatewayBase = 'http://127.0.0.1:8317';
  try {
    const url = new URL(normalizeApiBase(apiBase) || gatewayBase);
    if (url.protocol === 'http:' || url.protocol === 'https:') {
      url.username = '';
      url.password = '';
      url.search = '';
      url.hash = '';
      gatewayBase = url.toString().replace(/\/+$/, '').replace(/\/v1$/, '');
    }
  } catch {
    // An unset or invalid connection uses the documented loopback default.
  }
  const endpoint = `${gatewayBase}/v1`;
  const configuration = `model = "YOUR_MODEL"
model_provider = "hopper_gateway"
model_reasoning_effort = "medium"

[model_providers.hopper_gateway]
name = "Hopper AI Gateway"
base_url = ${JSON.stringify(endpoint)}
model_catalog_url = ${JSON.stringify(`${endpoint}/models`)}
experimental_bearer_token = "YOUR_GATEWAY_KEY"
wire_api = "responses"
requires_openai_auth = true
supports_websockets = true

[features]
api_key_model_discovery = true`;
  const copyConfiguration = async () => {
    const copied = await copyToClipboard(configuration);
    showNotification(
      t(copied ? 'quick_start.copied' : 'quick_start.copy_failed'),
      copied ? 'success' : 'error'
    );
  };
  return (
    <div className={styles.page}>
      <header className={styles.head}>
        <h1>{t('quick_start.title')}</h1>
      </header>
      <ol className={styles.steps}>
        <li>
          <div className={styles.step}>
            <h2>{t('quick_start.accounts_title')}</h2>
            <p>{t('quick_start.accounts_body')}</p>
            <div className={styles.actions}>
              <Link className="btn btn-primary btn-sm" to="/oauth">
                {t('quick_start.oauth_action')}
              </Link>
              <Link className="btn btn-secondary btn-sm" to="/auth-files">
                {t('quick_start.credentials_action')}
              </Link>
            </div>
          </div>
        </li>
        <li>
          <div className={styles.step}>
            <h2>{t('quick_start.routing_title')}</h2>
            <p>{t('quick_start.routing_body')}</p>
            <pre className={styles.shortCode}>
              <code>{'routing:\n  strategy: reset-first\n  session-affinity: true'}</code>
            </pre>
            <p>{t('quick_start.websocket_body')}</p>
            <div className={styles.actions}>
              <Link className="btn btn-secondary btn-sm" to="/config">
                {t('quick_start.config_action')}
              </Link>
              <Link className="btn btn-secondary btn-sm" to="/auth-files">
                {t('quick_start.credentials_action')}
              </Link>
            </div>
          </div>
        </li>
        <li>
          <div className={styles.step}>
            <h2>{t('quick_start.quota_title')}</h2>
            <p>{t('quick_start.quota_body')}</p>
            <div className={styles.actions}>
              <Link className="btn btn-secondary btn-sm" to="/quota">
                {t('quick_start.quota_action')}
              </Link>
            </div>
          </div>
        </li>
        <li>
          <div className={styles.step}>
            <h2>{t('quick_start.client_title')}</h2>
            <p>{t('quick_start.client_body')}</p>
            <div className={styles.codeHead}>
              <code>~/.codex/config.toml</code>
              <Button size="sm" variant="secondary" onClick={() => void copyConfiguration()}>
                {t('quick_start.copy_config')}
              </Button>
            </div>
            <pre className={styles.configuration}>
              <code>{configuration}</code>
            </pre>
            <p className={styles.privateAccess}>{t('quick_start.private_access')}</p>
            <div className={styles.actions}>
              <a
                href="https://help.router-for.me/agent-client/codex"
                target="_blank"
                rel="noopener noreferrer"
              >
                {t('quick_start.client_docs')}
              </a>
              <a
                href="https://tailscale.com/kb/1312/serve"
                target="_blank"
                rel="noopener noreferrer"
              >
                {t('quick_start.tailscale_action')}
              </a>
            </div>
          </div>
        </li>
      </ol>
    </div>
  );
}
