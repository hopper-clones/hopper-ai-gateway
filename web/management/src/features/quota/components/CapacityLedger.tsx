import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { CapacityAccount, CapacitySnapshot } from '@/services/api/capacity';
import {
  capacityGroups,
  capacityProviderName,
  currentCapacityRemaining,
  lifetimeCoverage,
} from '../capacityModel';
import { quotaTone } from '../quotaLedgerModel';
import styles from './QuotaLedger.module.scss';
import extra from './CapacityLedger.module.scss';

function AccountRow({ account, now }: { account: CapacityAccount; now: number }) {
  const { t, i18n } = useTranslation();
  const date = (value: string | null) =>
    value ? new Date(value).toLocaleString(i18n.resolvedLanguage) : t('capacity.unknown');
  const hasCredits =
    account.resetCredits !== null ||
    account.resetCreditsAvailable !== null ||
    account.usageCredits !== null;
  return (
    <article className={styles.row}>
      <div className={styles.identity}>
        <strong>{account.label}</strong>
        <small className={styles.observed}>
          {account.observedAt ? date(account.observedAt) : t('capacity.no_reading')}
        </small>
        <span>
          {account.plan} {account.plan ? ' · ' : ''}
          {t(`capacity.state_${account.freshness}`, { defaultValue: account.freshness })}
        </span>
      </div>
      <div className={styles.windows}>
        {account.meters.map((meter) => {
          const remaining = currentCapacityRemaining(account, meter, now);
          return (
            <div key={meter.id} className={styles.window}>
              <div className={styles.windowHead}>
                <span>
                  {meter.label}
                  {meter.model ? ` · ${meter.model}` : ''}
                </span>
                <strong>{remaining === null ? '—' : `${Math.round(remaining)}%`}</strong>
              </div>
              <div className={styles.track}>
                <span
                  className={styles[quotaTone(remaining)]}
                  style={{ width: `${remaining ?? 0}%` }}
                />
              </div>
              <div className={styles.reset}>
                {t('capacity.reset_at', { time: date(meter.resetsAt) })}
                {remaining === null && meter.remaining !== null && (
                  <span> · {t('capacity.saved_remaining', { value: meter.remaining })}</span>
                )}
              </div>
            </div>
          );
        })}
        {!account.meters.length && <span className={styles.state}>{t('capacity.no_reading')}</span>}
        {hasCredits && (
          <details className={extra.credits}>
            <summary>
              {t('capacity.credits')} ·{' '}
              {t('capacity.reset_count', {
                count: account.resetCreditsAvailable ?? undefined,
                value: account.resetCreditsAvailable ?? t('capacity.unknown'),
              })}
            </summary>
            {account.resetCredits?.map((credit) => (
              <p key={credit.id}>
                {t(`capacity.credit_${credit.status}`, { defaultValue: credit.status })} ·{' '}
                {credit.expiryKnown
                  ? t('capacity.expires_at', { time: date(credit.expiresAt) })
                  : t('capacity.expiry_unknown')}
              </p>
            ))}
            {account.usageCredits?.map((credit) => (
              <p key={credit.limitId}>
                {credit.label} ·{' '}
                {credit.unlimited
                  ? t('capacity.unlimited')
                  : (credit.balance ?? t('capacity.unknown'))}
              </p>
            ))}
          </details>
        )}
      </div>
      <div className={styles.actions}>
        <span className={styles.state}>{t('capacity.observation_only')}</span>
      </div>
    </article>
  );
}

export function CapacityLedger({ snapshot, now }: { snapshot: CapacitySnapshot; now: number }) {
  const { t, i18n } = useTranslation();
  const [provider, setProvider] = useState('all');
  const groups = useMemo(() => capacityGroups(snapshot.accounts), [snapshot.accounts]);
  const number = (value: number | null) =>
    value === null ? '—' : value.toLocaleString(i18n.resolvedLanguage);
  const coverage = lifetimeCoverage(snapshot);
  const local = snapshot.tokens.local;
  const account = snapshot.tokens.account;
  return (
    <div className={extra.ledger}>
      <p className={extra.source}>
        {t(snapshot.source.mode === 'owner-http' ? 'capacity.owner_live' : 'capacity.saved_owner')}
        {' · '}
        {t('capacity.read_at', {
          time: new Date(snapshot.readAt).toLocaleString(i18n.resolvedLanguage),
        })}
      </p>
      <details className={extra.tokens} open>
        <summary>
          {t('capacity.tokens')} · <strong>{number(local.total)}</strong> ·{' '}
          {t('capacity.all_recorded')}
        </summary>
        <p>
          {t('capacity.local_coverage')} {local.legacyDetailUnavailable && t('capacity.legacy_gap')}
        </p>
        <dl className={extra.totals}>
          <div>
            <dt>{t('capacity.input')}</dt>
            <dd>{number(local.input)}</dd>
          </div>
          <div>
            <dt>{t('capacity.output')}</dt>
            <dd>{number(local.output)}</dd>
          </div>
          <div>
            <dt>{t('capacity.cached')}</dt>
            <dd>{number(local.cachedInput)}</dd>
          </div>
        </dl>
        <div className={extra.tokenRows}>
          {local.providers.map((row) => (
            <div key={row.key}>
              <span>{capacityProviderName(row.key)}</span>
              <strong>{number(row.total)}</strong>
            </div>
          ))}
        </div>
        <details className={extra.accountTokens}>
          <summary>
            {t('capacity.provider_tokens')} · {number(account.reportedTokens)} ·{' '}
            {t('capacity.reporting', {
              measured: account.coverage.reportedAccounts,
              count: account.coverage.requestedAccounts,
            })}
          </summary>
          <p>{t('capacity.separate_tokens')}</p>
          <p>
            {t('capacity.lifetime')} · <strong>{number(account.lifetimeTokens)}</strong> ·{' '}
            {t('capacity.reporting', { measured: coverage.measured, count: coverage.total })}
          </p>
          <p>{t('capacity.fresh_reports', { count: account.coverage.freshAccounts })}</p>
          {account.accounts.map((row) => (
            <div className={extra.accountTokenRow} key={row.id}>
              <span>
                {snapshot.accounts.find((a) => a.id === row.id)?.label ??
                  capacityProviderName(row.provider)}
              </span>
              <span>
                {number(row.lifetimeTokens)} ·{' '}
                {t(`capacity.state_${row.state}`, { defaultValue: row.state })}
              </span>
            </div>
          ))}
        </details>
      </details>
      <nav className={extra.providers} aria-label={t('capacity.provider_filter')}>
        <button type="button" aria-pressed={provider === 'all'} onClick={() => setProvider('all')}>
          {t('auth_files.filter_all')} <small>{snapshot.accounts.length}</small>
        </button>
        {groups.map(([name, rows]) => (
          <button
            key={name}
            type="button"
            aria-pressed={provider === name}
            onClick={() => setProvider(name)}
          >
            {capacityProviderName(name)} <small>{rows.length}</small>
          </button>
        ))}
      </nav>
      {groups
        .filter(([name]) => provider === 'all' || provider === name)
        .map(([name, rows]) => (
          <section key={name} className={styles.group} aria-label={capacityProviderName(name)}>
            <h2>
              {capacityProviderName(name)} <span>{rows.length}</span>
            </h2>
            {rows.map((row) => (
              <AccountRow key={row.id} account={row} now={now} />
            ))}
          </section>
        ))}
    </div>
  );
}
