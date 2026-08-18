/**
 * Token Zen Design System — Warm Orange
 *
 * Based on B2B Design System "Warm Orange" palette:
 *   Primary:   #ED7B2F (orange)
 *   Secondary: #D54941 (朱红)
 *   Tertiary:  #F5BA18 (金黄)
 *   Neutrals:  warm gray (slightly amber-tinted)
 *
 * Three-layer architecture: Global → Alias → Component
 */
import type { ThemeConfig } from 'antd';

// ---------------------------------------------------------------------------
// 1. Global Tokens — Seed Colors & Palette
// ---------------------------------------------------------------------------

export const brand = {
  primary: '#ED7B2F',
  secondary: '#D54941',
  tertiary: '#F5BA18',
} as const;

/** Primary orange in 10 gradient steps */
export const primaryPalette = {
  50: '#FFF7F0',
  100: '#FFEDDB',
  200: '#FFD6B0',
  300: '#FFBC82',
  400: '#FF9E54',
  500: '#ED7B2F', // seed
  600: '#D06520',
  700: '#B04E14',
  800: '#8E3A0B',
  900: '#6B2805',
} as const;

/** Warm gray (amber-tinted neutrals for Orange/Red schemes) */
export const warmGray = {
  50: '#FAFAF8',
  100: '#F5F5F0',
  200: '#E8E8E2',
  300: '#D4D4CC',
  400: '#A8A89E',
  500: '#7A7A72',
  600: '#5C5C56',
  700: '#434340',
  800: '#2E2E2C',
  850: '#242422',
  900: '#1A1A18',
  950: '#121210',
} as const;

/** Fixed semantic colors (do not change with brand) */
export const semantic = {
  success: '#00A870',
  successBg: '#E8F8F0',
  successActive: '#008858',
  warning: '#F5BA18', // shifted from spec #ED7B2F to avoid primary collision
  warningBg: '#FFF8E6',
  warningActive: '#D9A200',
  error: '#E34D59',
  errorBg: '#FDECEE',
  errorActive: '#C9353F',
  info: '#0052D9',
  infoBg: '#ECF2FE',
  infoActive: '#003CAB',
} as const;

/** Chart / category extended palette */
export const extendedPalette = [
  '#ED7B2F', '#00A870', '#0052D9', '#D54941', '#F5BA18',
  '#7C3AED', '#0594FA', '#2BA471', '#E37318', '#029CD4',
  '#8C5AF2', '#D06520',
] as const;

// ---------------------------------------------------------------------------
// 2. Alias Tokens — Semantic Mappings
// ---------------------------------------------------------------------------

export const alias = {
  bgPage: warmGray[50],
  bgContainer: '#FFFFFF',
  bgContainerHover: warmGray[50],
  bgComponent: warmGray[100],
  bgBrand: primaryPalette[500],
  bgBrandHover: primaryPalette[600],

  textPrimary: warmGray[900],
  textSecondary: warmGray[600],
  textPlaceholder: warmGray[400],
  textDisabled: warmGray[300],
  textBrand: primaryPalette[600],

  borderDefault: warmGray[200],
  borderHover: warmGray[300],
} as const;

// ---------------------------------------------------------------------------
// 3. Ant Design Theme Config
// ---------------------------------------------------------------------------

/** Shared token overrides for Ant Design ConfigProvider */
const sharedToken: ThemeConfig['token'] = {
  colorPrimary: brand.primary,
  colorSuccess: semantic.success,
  colorWarning: semantic.warning,
  colorError: semantic.error,
  colorInfo: semantic.info,

  // Border radius: B2B conservative (md = 6px)
  borderRadius: 6,
  borderRadiusSM: 3,
  borderRadiusLG: 8,

  // Typography
  fontFamily:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif',
  fontFamilyCode:
    '"JetBrains Mono", "Fira Code", "Cascadia Code", monospace',

  // Neutrals — warm gray
  colorBgLayout: warmGray[50],
  colorBgContainer: '#FFFFFF',
  colorBorder: warmGray[200],
  colorBorderSecondary: warmGray[100],
  colorTextBase: warmGray[900],

  // Shadows — MUI-style dual-layer
  boxShadow:
    '0 1px 3px 0 rgba(0,0,0,0.05), 0 1px 2px -1px rgba(0,0,0,0.05)',
  boxShadowSecondary:
    '0 4px 12px 0 rgba(0,0,0,0.08), 0 2px 4px -2px rgba(0,0,0,0.05)',
};

/** Component-level overrides */
const sharedComponents: ThemeConfig['components'] = {
  Button: {
    controlHeight: 36,
    fontWeight: 500,
  },
  Card: {
    paddingLG: 20,
  },
  Table: {
    headerBg: warmGray[50],
    headerColor: warmGray[700],
    rowHoverBg: primaryPalette[50],
    borderColor: warmGray[100],
  },
  Menu: {
    itemBorderRadius: 6,
    itemMarginInline: 8,
    itemHeight: 40,
    iconSize: 18,
  },
  Input: {
    controlHeight: 36,
  },
  Select: {
    controlHeight: 36,
  },
};

/** Admin theme — dark sidebar, professional density */
export const adminTheme: ThemeConfig = {
  token: sharedToken,
  components: {
    ...sharedComponents,
    Table: {
      ...sharedComponents.Table,
      headerBg: warmGray[50],
      fontSize: 13,
    },
  },
};

/** Portal theme — lighter, friendlier */
export const portalTheme: ThemeConfig = {
  token: sharedToken,
  components: sharedComponents,
};

// ---------------------------------------------------------------------------
// 4. Layout Style Constants
// ---------------------------------------------------------------------------

/** Admin dark sidebar styles */
export const adminSidebarStyles = {
  background: warmGray[900],
  headerGradient: `linear-gradient(135deg, ${primaryPalette[600]}, ${primaryPalette[500]})`,
  menuItemSelectedBg: 'rgba(237, 123, 47, 0.15)',
  menuItemSelectedColor: primaryPalette[400],
  menuItemColor: warmGray[400],
  menuItemHoverBg: 'rgba(255, 255, 255, 0.06)',
} as const;

/** Portal light sidebar styles */
export const portalSidebarStyles = {
  background: '#FFFFFF',
  headerGradient: `linear-gradient(135deg, ${primaryPalette[500]}, ${primaryPalette[600]})`,
} as const;

/** Login page gradient background */
export const loginGradient = {
  background: `linear-gradient(135deg, ${primaryPalette[700]} 0%, ${primaryPalette[500]} 50%, ${primaryPalette[400]} 100%)`,
  glowPrimary: `radial-gradient(circle at 30% 40%, rgba(237, 123, 47, 0.2), transparent 60%)`,
  glowSecondary: `radial-gradient(circle at 70% 60%, rgba(245, 186, 24, 0.12), transparent 55%)`,
} as const;
