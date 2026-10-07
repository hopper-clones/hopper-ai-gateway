import { afterEach, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import '../src/i18n/index';
import { QuickStartPage } from '../src/pages/QuickStartPage';
import { useAuthStore } from '../src/stores';

const initialState = useAuthStore.getInitialState();
const initialBase = initialState.apiBase;
const initialKey = initialState.managementKey;
afterEach(() => {
  initialState.apiBase = initialBase;
  initialState.managementKey = initialKey;
});

function renderPage(apiBase: string) {
  initialState.apiBase = apiBase;
  initialState.managementKey = 'actual-management-secret-must-not-leak';
  return renderToStaticMarkup(createElement(MemoryRouter, null, createElement(QuickStartPage)));
}

function codexConfig(markup: string) {
  const blocks = [...markup.matchAll(/<pre[^>]*><code>([\s\S]*?)<\/code><\/pre>/g)];
  const text = blocks.map((match) => match[1]).find((block) => block.includes('model_provider'));
  expect(text).toBeDefined();
  return Bun.TOML.parse(
    text!
      .replace(/&quot;/g, '"')
      .replace(/&#x27;/g, "'")
      .replace(/&amp;/g, '&')
      .replace(/&lt;/g, '<')
      .replace(/&gt;/g, '>')
  );
}

describe('Gateway quick start', () => {
  test('provides usable native Responses WebSocket client configuration with loopback defaults', () => {
    const markup = renderPage('');
    const config = codexConfig(markup);
    expect(config.model).toBe('YOUR_MODEL');
    expect(config.model_provider).toBe('hopper_gateway');
    const provider = config.model_providers.hopper_gateway;
    expect(provider.base_url).toBe('http://127.0.0.1:8317/v1');
    expect(provider.model_catalog_url).toBe('http://127.0.0.1:8317/v1/models');
    expect(provider.experimental_bearer_token).toBe('YOUR_GATEWAY_KEY');
    expect(provider.wire_api).toBe('responses');
    expect(provider.supports_websockets).toBe(true);
    expect(config.features.api_key_model_discovery).toBe(true);
    expect(markup).not.toContain('actual-management-secret-must-not-leak');
    for (const path of ['/oauth', '/auth-files', '/quota', '/config'])
      expect(markup).toContain(`href="${path}"`);
    expect(markup).toContain('https://tailscale.com/kb/1312/serve');
  });

  test('uses the connected private gateway and removes management suffix from client URLs', () => {
    const config = codexConfig(renderPage('https://gateway.example.ts.net/tenant/v8/management/'));
    expect(config.model_providers.hopper_gateway.base_url).toBe(
      'https://gateway.example.ts.net/tenant/v1'
    );
    expect(config.model_providers.hopper_gateway.model_catalog_url).toBe(
      'https://gateway.example.ts.net/tenant/v1/models'
    );
  });

  test('does not duplicate the client API suffix or expose URL credentials', () => {
    const markup = renderPage(
      'https://private-user:private-password@gateway.example.ts.net/v1/?secret=private-query#private-fragment'
    );
    const config = codexConfig(markup);
    expect(config.model_providers.hopper_gateway.base_url).toBe(
      'https://gateway.example.ts.net/v1'
    );
    for (const secret of ['private-user', 'private-password', 'private-query', 'private-fragment'])
      expect(markup).not.toContain(secret);
  });
});
