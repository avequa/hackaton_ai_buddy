import { Icon, type IconName } from "../Icon";
import type { ModuleId } from "../context";
import { useCtx } from "../context";
import { firstName, greeting } from "../lib";
import { Topbar } from "../parts";

export const modules = [
  {
    id: "access" as ModuleId,
    icon: "shield" as IconName,
    index: "01",
    title: "Доступы",
    label: "Access Manager",
    description:
      "Получите нужные права по описанию задачи — без поиска владельцев и ручных согласований.",
    examples: ["GitLab", "Grafana", "Jira"],
    color: "violet",
    prompts: [
      "Мне дали задачу поправить алерт в дашборде платежей",
      "Нужен доступ к репозиторию payments-service",
    ],
  },
  {
    id: "support" as ModuleId,
    icon: "headphones" as IconName,
    index: "02",
    title: "Техподдержка",
    label: "IT Assistant",
    description: "Решите проблему с VPN, сетью или рабочим окружением с пошаговой диагностикой.",
    examples: ["VPN", "Сеть", "Окружение"],
    color: "orange",
    prompts: ["Не получается подключиться к VPN", "Не запускается локальное окружение"],
  },
  {
    id: "equipment" as ModuleId,
    icon: "laptop" as IconName,
    index: "03",
    title: "Оборудование",
    label: "Equipment Manager",
    description: "Закажите, замените или верните технику с автоматической проверкой наличия.",
    examples: ["Ноутбук", "Монитор", "Периферия"],
    color: "green",
    prompts: ["Мне нужен монитор 27 дюймов", "Хочу заменить ноутбук — он сильно тормозит"],
  },
];

export function ModulesScreen({ selectModule }: { selectModule: (id: ModuleId) => void }) {
  const { user, feed, openStarterPack } = useCtx();
  const starterWaiting = feed.notes.some((n) => n.kind === "starter_pack" && !n.read);
  return (
    <main className="content-area modules-screen">
      <Topbar title={`${greeting()}, ${firstName(user.name)}`} subtitle="С чего начнём сегодня?" />
      <section className="module-hero">
        <div className="eyebrow">
          <Icon name="spark" size={15} /> AI IT-BUDDY
        </div>
        <h2>
          Решайте рабочие вопросы
          <br />
          <em>без лишней рутины</em>
        </h2>
        <p>
          Опишите задачу своими словами — я найду нужный путь, проверю права и подготовлю заявку.
        </p>
      </section>
      {user.role === "intern" && (
        <section className={`starter-card ${starterWaiting ? "highlight" : ""}`}>
          <span className="quick-icon">
            <Icon name="key" size={20} />
          </span>
          <div>
            <strong>Первый день? Подготовим стандартные доступы</strong>
            <p>VPN, GitLab, Jira и Confluence вашей команды — проверим, чего не хватает, и отправим одной кнопкой.</p>
          </div>
          <button className="primary-button" onClick={openStarterPack}>
            Стартовый набор <Icon name="arrow" size={16} />
          </button>
        </section>
      )}
      <section className="module-grid">
        {modules.map((module) => (
          <button
            key={module.id}
            className={`module-card ${module.color}`}
            onClick={() => selectModule(module.id)}
          >
            <div className="module-card-top">
              <span className="module-icon">
                <Icon name={module.icon} size={24} />
              </span>
              <span className="module-index">{module.index}</span>
            </div>
            <span className="module-label">{module.label}</span>
            <h3>{module.title}</h3>
            <p>{module.description}</p>
            <div className="module-tags">
              {module.examples.map((example) => (
                <span key={example}>{example}</span>
              ))}
            </div>
            <div className="module-action">
              Начать диалог{" "}
              <span>
                <Icon name="arrow" size={18} />
              </span>
            </div>
          </button>
        ))}
      </section>
      <section className="quick-start">
        <span className="quick-icon">
          <Icon name="spark" size={20} />
        </span>
        <div>
          <strong>Не знаете, какой модуль выбрать?</strong>
          <p>Просто опишите вопрос — Buddy сам определит, кому его передать.</p>
        </div>
        <button onClick={() => selectModule("support")}>
          Задать вопрос <Icon name="arrow" size={16} />
        </button>
      </section>
    </main>
  );
}
