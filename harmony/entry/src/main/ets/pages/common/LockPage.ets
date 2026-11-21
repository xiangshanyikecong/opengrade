// 应用锁屏:PIN 数字键盘 + 可选生物识别(指纹/人脸,UserAuthKit 系统弹窗)。
// 冷启动经 Index 门禁进入(带 next 目标页),后台回切由 EntryAbility 拉起(解锁即 back)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { AppLock } from '../../common/AppLock';
import { OgC } from '../../common/OgTheme';

interface LockParams {
  next: string;
}

@Entry
@Component
struct LockPage {
  @State pin: string = '';
  @State msg: string = '';
  @State nextUrl: string = '';
  @State bioBusy: boolean = false;
  private maxLength: number = 6;

  aboutToAppear(): void {
    const p = router.getParams() as LockParams | null;
    if (p !== null && p !== undefined && p.next !== undefined) {
      this.nextUrl = p.next;
    }
    // 已开生物识别则自动拉起一次
    if (AppLock.biometricOn() && AppLock.canBiometric()) {
      this.startBiometric();
    }
  }

  private unlock(): void {
    const target = this.nextUrl;
    AppLock.takeLockIntent(); // 清残留意图,避免解锁后又被前台回调锁回去
    if (target !== '') {
      router.replaceUrl({ url: target }).catch(() => {
        router.back();
      });
    } else {
      router.back();
    }
  }

  private async submit(): Promise<void> {
    if (this.pin.length < 4) {
      return;
    }
    const ok = await AppLock.verify(this.pin);
    if (ok) {
      this.unlock();
    } else {
      this.msg = 'PIN 不正确,请重试';
      this.pin = '';
    }
  }

  private tapDigit(d: number): void {
    if (this.pin.length >= this.maxLength) {
      return;
    }
    this.msg = '';
    this.pin += String(d);
  }

  private del(): void {
    if (this.pin.length > 0) {
      this.pin = this.pin.substring(0, this.pin.length - 1);
    }
  }

  private startBiometric(): void {
    if (this.bioBusy) {
      return;
    }
    this.bioBusy = true;
    AppLock.biometricUnlock((): void => {
      this.bioBusy = false;
      this.unlock();
    }, (msg: string): void => {
      this.bioBusy = false;
      this.msg = msg;
    });
  }

  @Builder
  dot(filled: boolean) {
    Row()
      .width(14)
      .height(14)
      .borderRadius(7)
      .backgroundColor(filled ? OgC.BRAND : Color.Transparent)
      .border({ width: 2, color: filled ? OgC.BRAND : OgC.INK_SUB })
  }

  @Builder
  keyBtn(label: string, onTap: () => void) {
    Text(label)
      .fontSize(24)
      .fontWeight(FontWeight.Medium)
      .fontColor(OgC.INK)
      .width(72)
      .height(60)
      .textAlign(TextAlign.Center)
      .backgroundColor(OgC.CARD)
      .borderRadius(14)
      .onClick(() => onTap())
  }

  build() {
    Column({ space: 18 }) {
      Blank().height(30)
      Image($r('app.media.app_icon')).width(64).height(64).borderRadius(14)
      Text('OpenGrade 已锁定').fontSize(19).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
      Text('输入 PIN 解锁(4~6 位)').fontSize(13).fontColor(OgC.INK_SUB)
      Row({ space: 12 }) {
        this.dot(this.pin.length >= 1)
        this.dot(this.pin.length >= 2)
        this.dot(this.pin.length >= 3)
        this.dot(this.pin.length >= 4)
        this.dot(this.pin.length >= 5)
        this.dot(this.pin.length >= 6)
      }
      if (this.msg !== '') {
        Text(this.msg).fontSize(12).fontColor(OgC.BAD)
      }
      Column({ space: 10 }) {
        Row({ space: 10 }) {
          this.keyBtn('1', () => this.tapDigit(1))
          this.keyBtn('2', () => this.tapDigit(2))
          this.keyBtn('3', () => this.tapDigit(3))
        }
        Row({ space: 10 }) {
          this.keyBtn('4', () => this.tapDigit(4))
          this.keyBtn('5', () => this.tapDigit(5))
          this.keyBtn('6', () => this.tapDigit(6))
        }
        Row({ space: 10 }) {
          this.keyBtn('7', () => this.tapDigit(7))
          this.keyBtn('8', () => this.tapDigit(8))
          this.keyBtn('9', () => this.tapDigit(9))
        }
        Row({ space: 10 }) {
          if (AppLock.biometricOn() && AppLock.canBiometric()) {
            Text('🖐')
              .fontSize(22)
              .width(72)
              .height(60)
              .textAlign(TextAlign.Center)
              .onClick(() => this.startBiometric())
          } else {
            Blank().width(72).height(60)
          }
          this.keyBtn('0', () => this.tapDigit(0))
          Text('⌫')
            .fontSize(22)
            .fontColor(OgC.INK)
            .width(72)
            .height(60)
            .textAlign(TextAlign.Center)
            .backgroundColor(OgC.BG)
            .borderRadius(14)
            .onClick(() => this.del())
        }
      }
      .margin({ top: 6 })
      Button(this.pin.length >= 4 ? '解 锁' : '输入至少 4 位')
        .width(236)
        .height(44)
        .fontSize(16)
        .backgroundColor(this.pin.length >= 4 ? OgC.BRAND : OgC.LINE)
        .enabled(this.pin.length >= 4)
        .onClick(() => this.submit())
      Text('PIN 在「账号设置 → 应用锁」中设置;生物识别使用系统指纹/人脸。')
        .fontSize(11).fontColor(OgC.INK_SUB)
        .textAlign(TextAlign.Center)
        .padding({ left: 30, right: 30 })
      Blank()
    }
    .width('100%')
    .height('100%')
    .justifyContent(FlexAlign.Center)
    .backgroundColor(OgC.BG)
  }
}
