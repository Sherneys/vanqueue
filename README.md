# vanqueue — ระบบจัดสรรรถตู้ (Go + Supabase)

เว็บที่ทำงานเหมือน Google Sheet "ระบบจัดสรรรถตู้200คัน" (R6 — คิววนตามหมายเลขรถ)
สูตรทุกคอลัมน์ในชีตถูกเขียนใหม่เป็น Go ใน `engine.go` และมีเทสต์เทียบผลกับค่าที่ Google Sheets คำนวณ

| หน้า | เทียบกับชีต |
|---|---|
| `/` Dashboard | Dashboard + ข้อมูลกราฟ |
| `/queue` แนะนำรถวิ่งวันนี้ | แนะนำรถวิ่งวันนี้ (+ ติ๊กเลือกแล้วบันทึกว่าวิ่งแล้วได้ทันที) |
| `/logs` บันทึกการวิ่ง | บันทึกการวิ่งประจำวัน (ชื่อคนขับขึ้นเอง, คอลัมน์ตรวจสอบ) |
| `/vans` รายชื่อรถตู้ | รายชื่อรถตู้ (ช่องกรอก + ช่องสูตร) |
| `/settings` ตั้งค่า | ตั้งค่า |
| `/guide` คำแนะนำ | คำแนะนำการใช้งาน |

## ตั้งค่า Supabase

1. สร้างโปรเจกต์ใน [Supabase](https://supabase.com/dashboard)
2. กด **Connect** → เลือก **Session pooler** → คัดลอก URI
3. คัดลอก `.env.example` เป็น `.env` แล้ววาง URI ใน `DATABASE_URL` (แทน `[YOUR-PASSWORD]` ด้วยรหัสผ่านฐานข้อมูล)

4. ใส่ `SUPABASE_URL` และ `SUPABASE_PUBLISHABLE_KEY` (Project Settings → API Keys) สำหรับระบบล็อกอิน — ห้ามใส่ secret key

ตารางจะถูกสร้างอัตโนมัติตอนเปิดโปรแกรมครั้งแรก (`settings`, `vans`, `run_logs`)
และเปิด Row Level Security ไว้ เพื่อไม่ให้ใครเข้าถึงข้อมูลผ่าน REST API สาธารณะของ Supabase ได้

## ผู้ใช้และการล็อกอิน

ทุกหน้าต้องล็อกอินด้วยอีเมล + รหัสผ่านผ่าน Supabase Auth (ผู้ใช้ทุกคนมีสิทธิ์เท่ากัน)

- **เพิ่มผู้ใช้:** Supabase → Authentication → Users → Add user → Create new user (ติ๊ก Auto Confirm User)
- **ปิดการสมัครเอง:** Authentication → Sign In / Providers → ปิด "Allow new users to sign up" เพื่อให้มีแค่คนที่ผู้ดูแลเพิ่มเท่านั้น
- **ลบ/ระงับผู้ใช้:** ลบใน Authentication → Users (session เดิมจะหมดอายุภายใน 1 ชั่วโมง)

ถ้าเปิดเว็บให้ใช้ผ่านอินเทอร์เน็ต ให้ใช้ HTTPS (cookie จะถูกตั้งเป็น Secure อัตโนมัติเมื่อมาทาง HTTPS)

## ย้ายข้อมูลจาก Google Sheet (ครั้งแรก)

ใน Google Sheet: File → Download → Microsoft Excel (.xlsx) แล้วรัน

```bash
go run . -import ระบบจัดสรรรถตู้200คัน.xlsx
```

ถ้าฐานข้อมูลมีรถอยู่แล้ว ต้องเพิ่ม `-force` (จะลบข้อมูลเดิมทั้งหมดแล้วแทนที่ด้วยไฟล์)

## รัน

```bash
go run .
```

เปิด http://localhost:8080

## เทสต์

```bash
go test ./...
```

เทสต์เทียบกับชีตจริง: ตั้ง `FLEET_XLSX` (ไฟล์ .xlsx) และ `FLEET_EXPECTED` (CSV ค่าที่ชีตคำนวณ) ดูใน `engine_test.go`

เทสต์ฐานข้อมูล: ตั้ง `FLEET_TEST_DATABASE_URL` เป็น Postgres ทิ้งได้ (เทสต์จะลบข้อมูลทุกตาราง — ห้ามใช้ Supabase จริง)

## ข้อแตกต่างจากชีต

- รหัสรถที่ไม่มีชื่อคนขับ (แถวว่าง B-161–B-200 ในชีต) ไม่ถูกนำเข้า บันทึกการวิ่งที่ใช้รหัสพวกนี้จะขึ้น "รหัสไม่พบในรายชื่อ"
