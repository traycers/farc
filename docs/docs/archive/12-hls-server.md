# hls_server: HLS-сервис архивного воспроизведения

## 1. Назначение документа

Этот документ описывает `hls_server` — процесс, реализующий роль «Video Gateway» из системного контекста (`01-architecture.md`, диаграмма 6.1 в `11-service-composition.md`) для одного конкретного сценария: **просмотр архива farc в браузере через HLS**. `hls_server` — отдельный процесс, не часть `farcd`; общается с ним только через уже существующий внешний API (`HttpApiServer`, `EventPushServer`), без прямого доступа к Storage.

Область документа:
- Только воспроизведение архива (VOD). Live-просмотр текущего видеопотока с камер — отдельная задача, вне farc: обслуживается сторонним RTSP-прокси (например, mediamtx) до записи в архив, а не через `hls_server`.
- Кодеки в рамках документа — H.264 (видео) + AAC (аудио): оба совместимы с HLS/MSE нативно, `hls_server` выполняет только ремукс (перепаковку уже закодированных кадров в CMAF), без перекодирования. Другие кодеки, встречающиеся в `07-media-tree.md` (H.265, PCM, G.711), в текущей версии не поддерживаются — см. §8.

Документ **не описывает**:
- Внутреннее устройство `farcd` — оно уже описано в `02-storage.md`, `04-storage-operations.md`, `11-service-composition.md`; здесь `farcd` — чёрный ящик с уже определённым внешним API.
- Формат CMAF/fMP4 как таковой — это документированный внешний стандарт (ISO/IEC 14496-12), а не изобретение farc.
- Web-клиент (плеер в браузере) — потребитель API `hls_server`, его устройство вне области.

## 2. Терминология

Базовый глоссарий — `00-requirements.md` §2. Дополнительно к нему, специфично для этого документа:

- **hls_server** — процесс, описываемый этим документом; конкретная реализация роли «Video Gateway».
- **Сегмент** — единица HLS-плейлиста, CMAF media segment (`.m4s`), отдаётся `hls_server` одним HTTP-ответом.
- **Init-сегмент** — CMAF-ресурс с параметрами кодека (`init.mp4`), общий для всех медиа-сегментов одного fcontainer; ссылки на параметры кодека берутся из узлов `data`/`codec`/`param_sps`/`param_pps` медиа-дерева (`07-media-tree.md` §3.2–3.3).
- **Сетка сегментов** — последовательность границ времени внутри одного fcontainer, по которым `hls_server` режет его данные на сегменты (§6.2).

## 3. Место в системе

`hls_server` — внешний потребитель `farcd`, использующий ровно те же два канала, что и любой другой Video Gateway (`11-service-composition.md` §5.1.2–5.1.3):

- **`HttpApiServer`** — резолв кандидатов по `(канал, t1, t2)`, чтение данных фконтейнера по диапазонам (ADR-004), fallback-резолв `(uuid, канал, t1, t2)` → данные (ADR-016).
- **`EventPushServer`** — WebSocket-подписка на TOC по каналам (ADR-015): `hls_server` указывает при подключении интересующие каналы, получает TOC целиком вместе с `fblock.write.completed`, а также `fblock.deleted` при перезаписи.

`hls_server` не обращается к диску Storage напрямую и не участвует в приёме RTSP — это исключительная зона ответственности `farcd` (`ChannelIngest`, `11-service-composition.md` §5.1.1).

## 4. Карта компонентов

### 4.1. EventSubscriber

**Ответственность:** поддерживает WebSocket-подключение к `EventPushServer` сконфигурированного `farcd` (в v1 — ровно один, ADR-020), подписывается на TOC по всем каналам, которые обслуживает `hls_server`.

**Взаимодействие:**
- При успешном подключении (первом или после разрыва) инициирует бутстрап: для каждого канала запрашивает через `HttpApiServer` кандидатов и данные TOC за диапазон, не покрытый уже имеющимся индексом (ADR-016, тот же путь, что и для впервые подключившегося подписчика, — `11-service-composition.md` §5.1.3).
- Получив `fblock.write.completed` + TOC — передаёt запись в `ChannelIndex` (добавление).
- Получив `fblock.deleted` — передаёт UUID в `ChannelIndex` (инвалидация).
- Решение о построении локального индекса из push, а не резолве на каждый запрос плейлиста — ADR-018.

### 4.2. ChannelIndex

**Ответственность:** хранит в памяти, по каждому обслуживаемому каналу, упорядоченный по времени список записей `(fcontainer UUID, storage id, begin, end, разобранный TOC этого канала)`.

**Взаимодействие:**
- Обновляется `EventSubscriber`'ом (добавление/инвалидация записей).
- Отдаёт `PlaylistBuilder`'у список записей, пересекающих запрошенное окно `[t1, t2]` конкретного канала.

### 4.3. PlaylistBuilder

**Ответственность:** по запросу `(канал, t1, t2)` строит статический HLS-плейлист (VOD).

**Взаимодействие:**
- Запрашивает у `ChannelIndex` записи, пересекающие окно.
- Для каждой записи строит сетку сегментов **в границах одного fcontainer**, не пересекающую границу соседних записей (ADR-019) — точный алгоритм сетки § 6.2.
- Между сегментами, взятыми из двух разных fcontainer с отличающимся активным `config` (сменой SPS/PPS/ASC), вставляет `#EXT-X-DISCONTINUITY`.
- Не читает данные кадров — только метаданные из уже разобранного TOC в `ChannelIndex`.

### 4.4. SegmentBuilder

**Ответственность:** по запросу конкретного сегмента `(storage id, fcontainer UUID, номер сегмента внутри контейнера)` строит CMAF init-сегмент или медиа-сегмент.

**Взаимодействие:**
- Смещения нужных кадров и параметров кодека берёт из уже имеющегося в `ChannelIndex` TOC — отдельного резолва через ADR-016 для этого не требуется (TOC уже разобран `EventSubscriber`'ом при получении).
- Читает сами байты кадров у `HttpApiServer` по готовым смещениям (ADR-004).
- Мукширует кадры в CMAF (init-сегмент — из `param_sps`/`param_pps`/аудио-эквивалентов; медиа-сегмент — из `frame_data`/`frame_time`/`frame_kind` в границах сегмента).
- Передаёт результат в `SegmentCache` на сохранение.

### 4.5. SegmentCache

**Ответственность:** дисковый кэш готовых init- и медиа-сегментов.

**Взаимодействие:**
- Ключ — `(storage id, fcontainer UUID, номер сегмента)`; за счёт сетки, не пересекающей границу fcontainer (ADR-019), и привязки сетки к самому fcontainer, а не к произвольному окну запроса, разные запросы одного и того же участка архива (перемотка, повторный просмотр инцидента) обращаются к одному и тому же ключу.
- При инвалидации записи в `ChannelIndex` (`fblock.deleted`) уже закэшированные на диске сегменты не удаляются немедленно — данные физически скопированы в кэш и переживают перезапись оригинала в Storage; удаляются только по обычной политике вытеснения кэша (см. §8).
- Политика вытеснения — квота по объёму на диске, конкретный алгоритм (LRU и т.п.) — открытый вопрос, §8.

### 4.6. HttpServer

**Ответственность:** единственная точка входа для web-плеера.

**Взаимодействие:**
- `GET /channels/{канал}/hls/{t1}/{t2}/playlist.m3u8` → `PlaylistBuilder`.
- `GET /segments/{storage}/{uuid}/{номер}/init.mp4` и `.../seg.m4s` → сперва `SegmentCache` (попадание — отдать как есть), при промахе — `SegmentBuilder`, затем сохранение в `SegmentCache` и отдача.

## 5. Диаграммы

### 5.1. Контейнеры (C4, уровень 2)

```mermaid
C4Container
    title hls_server в системе farc

    Person(viewer, "Web-зритель")

    Container_Boundary(farc, "farcd / Archive") {
        Container(api, "HttpApiServer", "Go", "чтение по UUID/диапазонам, ADR-003/004/016")
        Container(push, "EventPushServer", "Go, WebSocket", "push TOC + события, ADR-015")
    }

    Container(hls, "hls_server", "Go", "строит HLS-плейлисты и сегменты из архива farc")

    Rel(hls, push, "подписка на TOC по каналам", "WebSocket")
    Rel(hls, api, "бутстрап-резолв (ADR-016), чтение диапазонов кадров (ADR-004)", "HTTP")
    Rel(viewer, hls, "GET playlist.m3u8 / *.m4s", "HTTP / HLS")
```

### 5.2. Компоненты hls_server (C4, уровень 3)

```mermaid
C4Component
    title Внутреннее устройство hls_server

    Container_Boundary(hls, "hls_server") {
        Component(sub, "EventSubscriber", "Go", "подписка на TOC, бутстрап через ADR-016")
        Component(idx, "ChannelIndex", "Go, in-memory", "канал → упорядоченный список fcontainer + TOC")
        Component(pb, "PlaylistBuilder", "Go", "строит VOD m3u8 по (канал, t1, t2)")
        Component(sb, "SegmentBuilder", "Go", "мукширует кадры в CMAF")
        ComponentDb(cache, "SegmentCache", "диск", "готовые init/media-сегменты")
        Component(http, "HttpServer", "Go", "HTTP API для плеера")
    }

    Rel(sub, idx, "добавление/инвалидация записей")
    Rel(pb, idx, "запрос записей за окно")
    Rel(http, pb, "запрос плейлиста")
    Rel(http, cache, "запрос сегмента")
    Rel(cache, sb, "промах кэша")
    Rel(sb, idx, "смещения и параметры кодека из TOC")
```

## 6. Потоки данных

### 6.1. Индексация TOC (push + бутстрап)

```mermaid
sequenceDiagram
    participant EP as EventPushServer (farcd)
    participant Sub as EventSubscriber
    participant API as HttpApiServer (farcd)
    participant Idx as ChannelIndex

    Note over Sub: при подключении (первом или после разрыва)
    Sub->>API: кандидаты + fallback-резолв по каждому каналу за непокрытый диапазон (ADR-016)
    API-->>Sub: TOC покрываемых fcontainer
    Sub->>Idx: добавить записи (бутстрап)

    Note over Sub,EP: далее — обычная работа
    EP->>Sub: event fblock.write.completed + TOC
    Sub->>Idx: добавить запись
    EP->>Sub: event fblock.deleted (UUID)
    Sub->>Idx: инвалидировать запись
```

### 6.2. Построение плейлиста

```mermaid
sequenceDiagram
    participant Player as Web-плеер
    participant Http as HttpServer
    participant PB as PlaylistBuilder
    participant Idx as ChannelIndex

    Player->>Http: GET .../{канал}/hls/{t1}/{t2}/playlist.m3u8
    Http->>PB: построить плейлист(канал, t1, t2)
    PB->>Idx: записи, пересекающие [t1, t2]
    Idx-->>PB: список fcontainer в порядке времени

    loop для каждого fcontainer из списка
        PB->>PB: сетка сегментов внутри границ этого fcontainer (ADR-019)
    end

    PB->>PB: между записями с разным активным config — вставить EXT-X-DISCONTINUITY
    PB-->>Http: m3u8 (статический, VOD)
    Http-->>Player: 200 + playlist.m3u8
```

### 6.3. Отдача сегмента

```mermaid
sequenceDiagram
    participant Player as Web-плеер
    participant Http as HttpServer
    participant Cache as SegmentCache
    participant SB as SegmentBuilder
    participant Idx as ChannelIndex
    participant API as HttpApiServer (farcd)

    Player->>Http: GET .../{storage}/{uuid}/{номер}/seg.m4s
    Http->>Cache: есть готовый сегмент?
    alt попадание в кэш
        Cache-->>Http: байты сегмента
    else промах
        Http->>SB: построить сегмент(storage, uuid, номер)
        SB->>Idx: смещения кадров + параметры кодека (уже разобранный TOC)
        SB->>API: читать диапазоны кадров (ADR-004)
        API-->>SB: байты кадров
        SB->>SB: мукс в CMAF media segment
        SB->>Cache: сохранить
    end
    Http-->>Player: 200 + seg.m4s
```

## 7. Конфигурация

Конфигурация процесса `hls_server` разделена между переменными окружения и одним JSON-файлом, по тому же признаку, что и у farc (`04-storage-operations.md` §2.1): статические параметры сервиса — в env, site-специфичные данные — в файле.

Переменные окружения (`HLS_SERVER_HTTP_IP`/`HLS_SERVER_HTTP_PORT`, `HLS_SERVER_FARC_HTTP`/`HLS_SERVER_FARC_WS`, `HLS_SERVER_TARGET_SEGMENT_DURATION`, `HLS_SERVER_CACHE_DIR`, `HLS_SERVER_CACHE_QUOTA_BYTES`):

- Адрес собственного HTTP-сервера `hls_server` (плеерный API).
- Единственный `farcd`, с которым работает `hls_server` (в v1 ровно один процесс, не список — ADR-020): адрес `HttpApiServer` (`HLS_SERVER_FARC_HTTP`), адрес `EventPushServer` (`HLS_SERVER_FARC_WS`).
- Целевая длительность сегмента (номинальная, до снапа на ближайший I-кадр и до границы fcontainer).
- Путь и квота дискового кэша сегментов.

JSON-файл:

- Список каналов, которые обслуживает `hls_server`, с указанием хранилища на этом `farcd` (та же связка `канал → хранилище`, что задана в конфигурации самого farcd, `11-service-composition.md` §9 — источник истины для этой связки между двумя процессами не определён, см. §8).

Сам JSON-файл хранится на Docker volume, а не в файле репозитория — как и `farc.config.json` (`04-storage-operations.md` §2.1). Volume изначально пуст; `hlsconfig.EnsureExists` при старте создаёт в нём пустой, но валидный конфиг (`{"channels": []}`), если файла ещё нет. В отличие от `farc.config.json`, `hls_server` не переписывает этот файл сам в рантайме (нет CRUD API для каналов) — добавление канала остаётся ручной операцией оператора.

## 8. Открытые вопросы

- **Кодеки за пределами H.264/AAC.** H.265 (видео) и PCM/G.711 (аудио) встречаются в `07-media-tree.md`, но не поддерживаются: H.265 плохо декодируется в Chrome/Firefox, PCM/G.711 не воспроизводится через MSE/HLS без AAC. Добавление потребует отдельного компонента транскодирования (видео H.265→H.264 и/или аудио →AAC) — вне области этого документа.
- **Политика вытеснения дискового кэша** — квота по объёму зафиксирована (§4.5, §7), конкретный алгоритм (LRU по времени последнего обращения, по размеру и т.п.) не выбран.
- **Схема восстановления после длинного разрыва WebSocket-соединения.** Бутстрап через ADR-016 (§6.1) закрывает случай «нет какого-либо индекса», но не специфицирует, как определить точную границу непокрытого диапазона после разрыва посреди работы (в отличие от первого подключения, где непокрытый диапазон — вся история канала) — тот же открытый вопрос, что и в ADR-015, применительно к TOC-индексу `hls_server`.
- **Источник истины для связки `канал → хранилище`.** Сейчас она задаётся конфигурацией `farcd` (`11-service-composition.md` §9) и должна быть продублирована в конфигурации `hls_server` (§7) — схема синхронизации (ручная, через API `farcd`, и т.д.) не определена. ADR-020 сужает v1 до одного `farcd`, но не решает эту проблему саму по себе — она остаётся открытой и для единственного `farcd`, и тем более встанет заново, когда `farcd` снова станет несколько.
