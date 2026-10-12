# Shopify Price Checker

Web app chạy local để đọc catalog công khai của Shopify, hiển thị thông tin sản phẩm, hình ảnh, giá từng variant và xuất snapshot `.xlsx`.

## Chạy bản đã build

1. Mở `shopify-price-checker.exe`.
2. Giữ cửa sổ console đang mở; dashboard sẽ tự mở trong trình duyệt tại địa chỉ `127.0.0.1`.
3. Nhập URL store, bấm **Kiểm tra store**, chọn phạm vi rồi bấm **Bắt đầu quét**.
4. Dùng bộ lọc trên bảng hoặc bấm **Tải Excel** khi scan hoàn tất.
5. Đóng bằng `Ctrl+C` trong cửa sổ console.

Không cần cài Go, Python, Node.js hay Shopify app. Tool không ghi dữ liệu vào store và không gửi dữ liệu tới server trung gian.

## Phạm vi dữ liệu

- Dữ liệu lấy từ sitemap, collection storefront và Ajax Product API công khai.
- Dashboard có bảng product riêng với title, description, vendor, type, tags, collections, options, ngày đăng, ảnh và khoảng giá.
- Bảng variant giữ một dòng cho mỗi variant; ảnh variant được ưu tiên khi storefront cung cấp.
- Ảnh được lưu dưới dạng URL/hyperlink và thumbnail, không tải file ảnh nhị phân về máy.
- Giá được đọc theo locale/currency mà URL hiện tại trả về.
- Tồn kho chỉ có trạng thái còn/hết/không rõ, không có số lượng chính xác.
- Không bao gồm draft, store có mật khẩu, giá sau coupon/checkout hoặc selling-plan price.
- Ajax Product API có thể giới hạn 250 variants cho một product; tool sẽ tạo cảnh báo nếu chạm ngưỡng này.

## File Excel

Workbook có năm sheet: `Summary`, `Products`, `Images`, `Variants`, `Errors`. `Products` chứa cả mô tả plain text và HTML; `Images` có một dòng cho mỗi ảnh. Các cột giá là số, không phải chuỗi, nên có thể tiếp tục lọc và tính toán trong Excel.

## Build từ source

Yêu cầu Go 1.27 trở lên:

```powershell
.\build.ps1
```

Script sẽ chạy toàn bộ test trước khi tạo `shopify-price-checker.exe`. Project chỉ dùng Go standard library; CSS và JavaScript được nhúng vào executable.

## API local

- `POST /api/discover`
- `POST /api/scans`
- `GET /api/scans/current`
- `POST /api/scans/current/cancel`
- `GET /api/scans/current/export.xlsx`

Server chỉ bind loopback và từ chối Host/remote address không phải local.
