# parser-fipi

Выгрузка заданий из открытого банка ФИПИ (ЕГЭ / ОГЭ) в JSON с картинками.

## Установка

```bash
go install github.com/sdn2s/parser-fipi@latest
```

## Примеры

```bash
parser-fipi -exam ege -subject math -n 5 -count 20     # 20 заданий №5 ЕГЭ (профиль)
parser-fipi -exam oge -subject math -n 10 -random      # 10 случайных №10 ОГЭ
parser-fipi -exam ege -subject phys -kes 3.1 -count 50 # по теме КЭС
parser-fipi -exam ege -subject math -id 40B442         # одно задание
parser-fipi -exam oge -list-subjects                   # список предметов
parser-fipi -exam ege -subject math -list-lines        # список номеров заданий
```

## Флаги

| Флаг | Описание |
|---|---|
| `-exam` | `ege` или `oge` |
| `-subject` | предмет: `math`, `math-base`, `phys`, `rus`, `inf`… |
| `-n` | номер задания в варианте |
| `-count` | количество заданий (по умолчанию 10, `0` — все) |
| `-random` | случайная выборка |
| `-kes`, `-kind` | фильтр по темам КЭС и типу ответа |
| `-id` | номер задания ФИПИ |
| `-out` | папка результата (по умолчанию `fipi_out/`) |
| `-no-images` | не скачивать картинки |

Полный список: `parser-fipi -h`.

## Ограничения

- Правильных ответов в банке ФИПИ нет.
- `-n` работает для профильной математики ЕГЭ и математики ОГЭ. Номер определяется по правилам из `mappings/`, поэтому возможны неточности.
- Материалы принадлежат ФИПИ, при использовании указывайте источник.
