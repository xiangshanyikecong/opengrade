// 共享轻量 UI 部件:标题栏、卡片、空态、徽标、行条目。

import { OgC, OgDim } from '../common/OgTheme';

// @BuilderParam 的默认值必须是 @Builder 函数(全局)。
@Builder
function emptyBuilder() {
}

@Component
export struct TitleBar {
  title: string = '';
  subtitle: string = '';
  onBack: () => void = () => {};
  showBack: boolean = true;
  @BuilderParam actions: () => void = emptyBuilder;

  build() {
    Row({ space: 8 }) {
      if (this.showBack) {
        Text('‹')
          .fontSize(26)
          .fontColor(OgC.BRAND)
          .width(36)
          .height(36)
          .textAlign(TextAlign.Center)
          .onClick(() => this.onBack())
      }
      Column({ space: 1 }) {
        Text(this.title).fontSize(17).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        if (this.subtitle !== '') {
          Text(this.subtitle).fontSize(12).fontColor(OgC.INK_SUB)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        }
      }.alignItems(HorizontalAlign.Start).layoutWeight(1)
      this.actions()
    }
    .width('100%')
    .padding({ left: 12, right: 12, top: 8, bottom: 8 })
    .backgroundColor(OgC.CARD)
  }
}

@Component
export struct PanelCard {
  @BuilderParam content: () => void = emptyBuilder;
  title: string = '';

  build() {
    Column({ space: 10 }) {
      if (this.title !== '') {
        Text(this.title).fontSize(14).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
      }
      this.content()
    }
    .alignItems(HorizontalAlign.Start)
    .width('100%')
    .padding(12)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
  }
}

@Component
export struct EmptyView {
  text: string = '暂无数据';

  build() {
    Column({ space: 8 }) {
      Text('· · ·').fontSize(28).fontColor(OgC.LINE)
      Text(this.text).fontSize(13).fontColor(OgC.INK_SUB)
    }
    .width('100%')
    .padding(40)
    .alignItems(HorizontalAlign.Center)
  }
}

@Component
export struct ErrorView {
  msg: string = '';
  onRetry: () => void = () => {};

  build() {
    Column({ space: 10 }) {
      Text('⚠ ' + this.msg).fontSize(13).fontColor(OgC.BAD).textAlign(TextAlign.Center)
      Button('重试')
        .fontSize(13)
        .backgroundColor(OgC.BRAND)
        .onClick(() => this.onRetry())
    }
    .width('100%')
    .padding(30)
    .alignItems(HorizontalAlign.Center)
  }
}

// 状态徽标(考试阶段/发布状态等)
@Component
export struct Pill {
  text: string = '';
  color: string = OgC.BRAND;

  build() {
    Text(this.text)
      .fontSize(11)
      .fontColor(this.color)
      .padding({ left: 8, right: 8, top: 3, bottom: 3 })
      .borderRadius(10)
      .backgroundColor(this.color + '1A') // 10% 透明底
      .maxLines(1)
  }
}

// 键值行
@Component
export struct KVRow {
  k: string = '';
  v: string = '';
  vColor: string = OgC.INK;

  build() {
    Row() {
      Text(this.k).fontSize(13).fontColor(OgC.INK_SUB)
      Blank()
      Text(this.v).fontSize(13).fontColor(this.vColor).fontWeight(FontWeight.Medium)
        .textAlign(TextAlign.End)
    }
    .width('100%')
    .padding({ top: 5, bottom: 5 })
  }
}

@Component
export struct PrimaryButton {
  label: string = '';
  usable: boolean = true; // 避免与 CustomComponent.enabled 通用属性重名
  fill: boolean = false; // true=Row 内 layoutWeight 均分
  full: boolean = false; // true=占满父容器宽度(Column 场景);都 false=按内容宽(Row 紧凑)
  onTap: () => void = () => {};

  @Builder
  btn() {
    Button(this.label)
      .fontSize(14)
      .backgroundColor(this.usable ? OgC.BRAND : OgC.LINE)
      .fontColor(this.usable ? Color.White : OgC.INK_SUB)
      .enabled(this.usable)
      .height(40)
      .onClick(() => this.onTap())
  }

  build() {
    if (this.fill) {
      Column() {
        this.btn()
      }
      .layoutWeight(1)
    } else if (this.full) {
      Column() {
        this.btn()
      }
      .width('100%')
    } else {
      this.btn()
    }
  }
}
@Component
export struct GhostButton {
  label: string = '';
  danger: boolean = false;
  onTap: () => void = () => {};

  build() {
    Button(this.label)
      .fontSize(13)
      .backgroundColor(Color.Transparent)
      .borderWidth(1)
      .borderColor(this.danger ? OgC.BAD : OgC.BRAND)
      .fontColor(this.danger ? OgC.BAD : OgC.BRAND)
      .height(34)
      .padding({ left: 12, right: 12 })
      .onClick(() => this.onTap())
  }
}

// 分区标题(面板内)
@Component
export struct SectionHeader {
  text: string = '';
  extra: string = '';

  build() {
    Row({ space: 8 }) {
      Row() { }.width(4).height(14).backgroundColor(OgC.BRAND).borderRadius(2)
      Text(this.text).fontSize(14).fontWeight(FontWeight.Bold).fontColor(OgC.INK).layoutWeight(1)
      if (this.extra !== '') {
        Text(this.extra).fontSize(12).fontColor(OgC.INK_SUB)
      }
    }
    .width('100%')
    .margin({ top: 6, bottom: 2 })
  }
}

// 卡片分区:标题 + @BuilderParam 内容(页面用 @Builder 方法引用初始化)。
@Component
export struct SectionCard {
  title: string = '';
  extra: string = '';
  @BuilderParam content: () => void = emptyBuilder;

  build() {
    Column({ space: 6 }) {
      SectionHeader({ text: this.title, extra: this.extra })
      this.content()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }
}

@Component
export struct LoadingView {
  text: string = '加载中…';

  build() {
    Column({ space: 10 }) {
      LoadingProgress().width(34).height(34).color(OgC.BRAND)
      Text(this.text).fontSize(13).fontColor(OgC.INK_SUB)
    }
    .width('100%')
    .padding(40)
    .alignItems(HorizontalAlign.Center)
  }
}

