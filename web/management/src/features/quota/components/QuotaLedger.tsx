import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { IconRefreshCw } from '@/components/ui/icons';
import type { ResolvedTheme } from '@/types';
import { formatRelativeInstant } from '@/utils/quota';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { getAuthFileIcon, getTypeLabel } from '@/features/authFiles/constants';
import { QUOTA_TAB_ORDER } from '../constants';
import type { QuotaFileEntry } from '../logic';
import { QUOTA_ADAPTERS, type QuotaCardState } from '../providers';
import { useClaudeResetGrants } from '../providers/claude/ClaudeResetGrants';
import {
  aggregateLedgerWindows,
  ledgerPlan,
  ledgerWindows,
  maskQuotaIdentity,
  quotaTone,
  type LedgerWindow,
} from '../quotaLedgerModel';
import { bindQuotaClasses } from '../types';
import bodyStyles from './QuotaBody.module.scss';
import styles from './QuotaLedger.module.scss';

const bodyClasses = bindQuotaClasses(bodyStyles, 'QuotaBody.module.scss');
interface Props {
  entries: QuotaFileEntry[];
  quotaFor: (entry: QuotaFileEntry) => QuotaCardState | undefined;
  resolvedTheme: ResolvedTheme;
  showEmails: boolean;
  canRefresh: boolean;
  resettingQuotaName: string | null;
  onRefresh: (entry: QuotaFileEntry) => void;
  onReset: (entry: QuotaFileEntry) => void;
  now: number;
}
const amount = (value: number | null) => (value == null ? '--' : `${Math.round(value)}%`);

function ResetTime({ instant, now }: { instant: number | null; now: number }) {
  const { t, i18n } = useTranslation();
  if (instant == null) return <span>{t('quota_management.ledger_no_reset')}</span>;
  const date = new Date(instant);
  const pad = (n: number) => String(n).padStart(2, '0');
  return (
    <time dateTime={date.toISOString()} title={date.toLocaleString(i18n.resolvedLanguage)}>
      {formatRelativeInstant(instant, now, i18n.resolvedLanguage)} · {pad(date.getMonth() + 1)}/
      {pad(date.getDate())}, {pad(date.getHours())}:{pad(date.getMinutes())}
    </time>
  );
}
function WindowMeter({ window, now }: { window: LedgerWindow; now: number }) {
  const { t } = useTranslation();
  return (
    <div className={`${styles.window} ${window.muted ? styles.muted : ''}`}>
      <div className={styles.windowHead}>
        <span>
          {window.labelKey
            ? t(window.labelKey, window.labelParams)
            : window.label || t('quota_management.ledger_weekly')}
        </span>
        <strong>{amount(window.remaining)}</strong>
      </div>
      <div className={styles.track}>
        <span
          className={styles[quotaTone(window.remaining)]}
          style={{ width: `${window.remaining ?? 0}%` }}
        />
      </div>
      <div className={styles.reset}>
        <ResetTime instant={window.resetAtMs} now={now} />
      </div>
    </div>
  );
}
function LedgerRow({
  entry,
  quotaFor,
  showEmails,
  canRefresh,
  resettingQuotaName,
  onRefresh,
  onReset,
  now,
}: Props & { entry: QuotaFileEntry }) {
  const { t } = useTranslation();
  const quota = quotaFor(entry);
  const [showDetails, setShowDetails] = useState(false);
  const detailsId = useId();
  const adapter = QUOTA_ADAPTERS[entry.type];
  const status = quota?.status ?? 'idle';
  const blocked =
    !canRefresh ||
    Boolean(entry.file.disabled) ||
    status === 'loading' ||
    resettingQuotaName === getQuotaCacheKey(entry.file);
  const reset = useClaudeResetGrants(
    entry.file,
    entry.type === 'claude' && status === 'success',
    blocked,
    quota,
    () => onRefresh(entry)
  );
  const windows = ledgerWindows(entry.type, quota);
  const name = showEmails ? entry.file.name : maskQuotaIdentity(entry.file.name);
  const plan = ledgerPlan(entry.file, quota);
  const translatedPlan = plan ? t(`${adapter.i18nPrefix}.${plan}`, { defaultValue: plan }) : null;
  const hasReset = quota && status === 'success' && adapter.canResetQuota?.(quota);
  return (
    <article className={`${styles.row} ${entry.file.disabled ? styles.disabled : ''}`}>
      <div className={styles.identity}>
        <strong>
          {quota && status === 'success' ? (
            <button
              className={styles.identityDetails}
              type="button"
              aria-label={`${name} · ${t('quota_management.ledger_details')}`}
              aria-expanded={showDetails}
              aria-controls={detailsId}
              onClick={() => setShowDetails(!showDetails)}
            >
              {name}
            </button>
          ) : name}
        </strong>
        {quota && 'observedAtMs' in quota && (
          <small className={styles.observed}>
            {t('quota_management.ledger_observed', {
              time: new Date(Number(quota.observedAtMs)).toLocaleString(),
            })}
            {'observationStale' in quota && quota.observationStale
              ? ` · ${t('quota_management.ledger_expired')}`
              : ''}
          </small>
        )}
        <span>
          {translatedPlan}
          {entry.file.disabled && ` · ${t('common.disabled')}`}
        </span>
      </div>
      <div className={styles.windows}>
        {quota?.routingObservation &&
          !['applied', 'superseded'].includes(quota.routingObservation.status) && (
            <p className={styles.routingNotice} role="status">
              {t('quota_management.ledger_not_routing', {
                status: t(`quota_management.ledger_routing_${quota.routingObservation.status}`),
              })}
              {quota.routingObservation.reason ? ` · ${quota.routingObservation.reason}` : ''}
            </p>
          )}

        {windows.map((window) => (
          <WindowMeter key={window.id} window={window} now={now} />
        ))}
        {!windows.length && (
          <span className={styles.state} role={status === 'error' ? 'alert' : undefined}>
            {status === 'error'
              ? t(`${adapter.i18nPrefix}.load_failed`, {
                  message: quota?.error ?? t('common.unknown_error'),
                })
              : t(
                  `${adapter.i18nPrefix}.${status === 'loading' ? 'loading' : status === 'success' ? 'empty_data' : 'idle'}`
                )}
          </span>
        )}
      </div>
      <div className={styles.actions}>
        <button type="button" onClick={() => onRefresh(entry)} disabled={blocked || reset.busy}>
          <IconRefreshCw size={14} />
          {t('auth_files.quota_refresh_single')}
        </button>
        {entry.type === 'claude' && !reset.blocked && (
          <button type="button" onClick={reset.confirm} disabled={reset.blocked}>
            {t(`claude_reset.${reset.buttonLabel}`)}
            {reset.count != null && ` (${reset.count})`}
          </button>
        )}
        {hasReset && (
          <button type="button" onClick={() => onReset(entry)} disabled={blocked}>
            {t('codex_quota.reset_button')}
          </button>
        )}
      </div>
      {quota && status === 'success' && showDetails && (
        <div className={styles.details} id={detailsId}>
          <adapter.Body quota={quota} classes={bodyClasses} />
          {reset.message && (
            <small className={styles.resetNotice} role="status">
              {t(`claude_reset.${reset.message}`)}
            </small>
          )}
        </div>
      )}
    </article>
  );
}

export function QuotaLedger(props: Props) {
  const { t } = useTranslation();
  const [showBroad, setShowBroad] = useState(false);
  const groups = QUOTA_TAB_ORDER.map((type) => ({
    type,
    entries: props.entries.filter((entry) => entry.type === type),
  })).filter((group) => group.entries.length > 0);
  return (
    <>
      <section className={styles.summaries} aria-label={t('quota_management.ledger_summary')}>
        {groups.map((group) => {
          const rows = group.entries.map((entry) =>
            ledgerWindows(entry.type, props.quotaFor(entry))
          );
          const summary = aggregateLedgerWindows(rows);
          const broad = group.type === 'claude' ? aggregateLedgerWindows(rows, 'seven-day') : null;
          return (
            <div key={group.type} className={styles.summary}>
              <div className={styles.summaryHead}>
                <span>
                  <img src={getAuthFileIcon(group.type, props.resolvedTheme) ?? undefined} alt="" />
                  {getTypeLabel(t, group.type)}
                </span>
                <small>
                  {t('quota_management.meta_credentials', { count: group.entries.length })}
                </small>
              </div>
              <div className={styles.summaryLabel}>
                {summary.window?.labelKey
                  ? t(summary.window.labelKey, summary.window.labelParams)
                  : summary.window?.label || t('quota_management.ledger_weekly')}
              </div>
              <div className={styles.total}>
                <strong>{amount(summary.remaining)}</strong>
                <span>{t('quota_management.ledger_of', { capacity: summary.capacity })}</span>
              </div>
              <div className={styles.segments}>
                {summary.segments.map((window, index) => (
                  <div className={styles.track} key={index}>
                    <span
                      className={styles[quotaTone(window?.remaining ?? null)]}
                      style={{ width: `${window?.remaining ?? 0}%` }}
                    />
                  </div>
                ))}
              </div>
              <div className={styles.reset}>
                <ResetTime instant={summary.resetAtMs} now={props.now} />
              </div>
              {summary.measuredCount > 0 && summary.measuredCount !== group.entries.length && (
                <small className={styles.coverage}>
                  {t('quota_management.ledger_measured', {
                    measured: summary.measuredCount,
                    count: group.entries.length,
                  })}
                </small>
              )}
              {broad?.window && (
                <div className={styles.secondary}>
                  <span>
                    {t('claude_quota.seven_day')} <strong>{amount(broad.remaining)}</strong>
                  </span>
                  <button
                    type="button"
                    aria-pressed={showBroad}
                    onClick={() => setShowBroad(!showBroad)}
                  >
                    {t(`quota_management.ledger_${showBroad ? 'hide' : 'show'}`)}
                  </button>
                  {showBroad && (
                    <WindowMeter
                      window={{
                        ...broad.window,
                        remaining:
                          broad.remaining == null ? null : broad.remaining / group.entries.length,
                        resetAtMs: broad.resetAtMs,
                      }}
                      now={props.now}
                    />
                  )}
                </div>
              )}
            </div>
          );
        })}
      </section>
      {groups.map((group) => (
        <section key={group.type} className={styles.group} aria-label={getTypeLabel(t, group.type)}>
          <h2>
            {getTypeLabel(t, group.type)} <span>{group.entries.length}</span>
          </h2>
          {group.entries.map((entry) => (
            <LedgerRow key={getQuotaCacheKey(entry.file)} {...props} entry={entry} />
          ))}
        </section>
      ))}
    </>
  );
}
