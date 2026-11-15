import { useState } from "react";
import { Icon } from "./Icon";

export const BRAND_NAME = "AvitoBuddy";
export const BRAND_TAGLINE = "Адаптация стажёров";

// Логотип: public/brand-logo.png. Нет файла - значок-искра.
const LOGO_SRC = "/brand-logo.png";

export function BrandMark({ size = 23 }: { size?: number }) {
  const [failed, setFailed] = useState(false);
  if (failed) {
    return (
      <span className="brand-mark">
        <Icon name="spark" size={size} />
      </span>
    );
  }
  return (
    <span className="brand-mark logo">
      <img src={LOGO_SRC} alt="" onError={() => setFailed(true)} />
    </span>
  );
}
