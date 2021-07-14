# golang-deep-learn
加深学习golang的工具和一些编程技巧

### 置顶：CIDI 从零开始

详情见项目 CIDI目录

#### 我们如何确保某个类型实现了某个接口的所有方法呢？一般可以使用下面的方法进行检测，如果实现不完整，编译期将会报错。
    var _ Person = (*Student)(nil)
    var _ Person = (*Worker)(nil)
